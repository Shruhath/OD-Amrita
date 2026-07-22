// Command api is the OD-Amrita HTTP server entrypoint (gin). One of two api/
// entrypoints; cmd/worker runs the persistent River worker.
//
// Transport security (NFR15): the server is deployed behind TLS 1.2+ ONLY.
// Plain HTTP is REJECTED, not redirected — there is deliberately NO HTTP→HTTPS
// redirect listener: no plain-port listener and no 301 redirect handler. Do not
// add one. TLS_ENABLED=false exists solely for ingress-terminated deployments
// where the platform enforces TLS (and must also reject, not redirect).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Shruhath/OD-Amrita/api/internal/config"
	handleradmin "github.com/Shruhath/OD-Amrita/api/internal/handler/admin"
	handlerauth "github.com/Shruhath/OD-Amrita/api/internal/handler/auth"
	handlerevent "github.com/Shruhath/OD-Amrita/api/internal/handler/event"
	"github.com/Shruhath/OD-Amrita/api/internal/handler/health"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	corsmw "github.com/Shruhath/OD-Amrita/api/internal/middleware/cors"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	svcuser "github.com/Shruhath/OD-Amrita/api/internal/service/user"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("api: %v", err)
	}
}

// run wires config → runtime pool → gin → /health → TLS serve, then blocks
// until a shutdown signal or a serve error. It returns (rather than calling
// log.Fatal inline) so its defers — notably the pool drain — always execute.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	pool, err := store.NewPool(context.Background(), cfg.DatabaseURLRuntime)
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.AppEnv != "development" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(errormw.Handler())
	router.Use(corsmw.Middleware(cfg.PortalAllowedOrigins))
	router.GET("/health", health.Handler(pool))

	var verifier svcauth.TokenVerifier = svcauth.RejectingVerifier{}
	if cfg.Auth.JWKSURL != "" && cfg.Auth.Issuer != "" {
		v, err := svcauth.NewVerifier(cfg.Auth.JWKSURL, cfg.Auth.Issuer, cfg.Auth.Audience)
		if err != nil {
			return err
		}
		verifier = v
	}
	// The write boundary + first-login provisioning service. The store binds the
	// od_runtime pool; the service co-commits a user row and its user.provisioned
	// audit row through store.WithAudit at the /auth/me login round-trip. In a
	// no-DB dev boot the pool connects lazily (pgxpool), so the server still boots
	// and /health reports db:down — provisioning simply fails with a centralized
	// 5xx until a live DB is present (D6). Provisioning is never reached anyway
	// until Supabase auth is configured (RejectingVerifier 401s every call).
	st := store.New(pool)
	userSvc := svcuser.NewService(st, cfg.Auth.BootstrapAdmins, svcuser.NewSQLCProvisioner())

	// Auth-event auditor (Story 1.7): co-commits auth.login_succeeded (from
	// /auth/me, after provisioning) and auth.domain_rejected (from the auth
	// middleware's EnforceDomain failure branch) through the same store.WithAudit
	// boundary. One value serves both the middleware and the handler.
	eventAuditor := svcauth.NewEventAuditor(st)

	// Forced-logout (Story 1.5c): the revocation checker the auth middleware
	// consults on every protected request, and the admin-only revocation service.
	// Supabase exposes no supported server-side target-user revoke API, so the
	// Supabase adapter is the disabled no-op — the local deny-list is authoritative.
	revChecker := svcauth.NewSQLCRevocationChecker(pool)
	revSvc := svcauth.NewRevocationService(
		st,
		svcauth.NewSQLCRevocationWriter(),
		svcauth.DisabledRevoker{},
		svcauth.NewSQLCUserDirectory(pool),
	)

	api := router.Group("/api/v1")
	api.Use(mwauth.Middleware(verifier, cfg.Auth.AllowedDomain, eventAuditor))
	// Reject revoked-but-still-valid tokens on the NEXT request, immediately after
	// verify+domain and before any protected handler runs (DoD 6).
	api.Use(mwauth.RevocationGuard(revChecker))
	api.GET("/auth/me", handlerauth.MeHandler(userSvc, eventAuditor))

	admin := api.Group("/admin")
	admin.Use(rbac.RequireAdmin(rbac.NewSQLCPrincipalLoader(pool)))
	admin.POST("/users/:user_id/sessions/revoke", handleradmin.RevokeSessionsHandler(revSvc))
	eventSvc := svcevent.NewService(svcevent.NewSQLCReader(pool), st, svcevent.NewSQLCWriter())
	handlerevent.RegisterAdminRoutes(admin, eventSvc)

	srv := &http.Server{
		Addr:    ":" + cfg.APIPort,
		Handler: router,
		// Bound every phase of a request so a slow/idle client can't pin a
		// connection (goroutine) indefinitely — Slowloris hardening.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Serve in the background so the main goroutine can wait for a signal and
	// shut down cleanly (log.Fatal would os.Exit and skip the pool drain).
	serveErr := make(chan error, 1)
	go func() {
		if cfg.TLS.Enabled {
			srv.TLSConfig = &tls.Config{MinVersion: tlsMinVersion(cfg.TLS.MinVersion)}
			log.Printf("api: serving TLS-only on %s (min TLS %s)", srv.Addr, cfg.TLS.MinVersion)
			serveErr <- srv.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile)
			return
		}
		// Ingress-terminated mode: the single listener, no redirect (NFR15).
		// This serves PLAINTEXT and is safe ONLY behind a TLS-terminating
		// ingress — warn loudly so an accidental TLS_ENABLED=false is visible.
		log.Printf("api: WARNING serving PLAINTEXT HTTP on %s — TLS_ENABLED=false; safe ONLY behind a TLS-terminating ingress (NFR15)", srv.Addr)
		serveErr <- srv.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case sig := <-stop:
		log.Printf("api: received %s, draining", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}

// tlsMinVersion maps the configured floor to the crypto/tls constant. config
// has already validated the value to "1.2" or "1.3", so the floor is never
// below TLS 1.2 (NFR15).
func tlsMinVersion(v string) uint16 {
	if v == "1.3" {
		return tls.VersionTLS13
	}
	return tls.VersionTLS12
}
