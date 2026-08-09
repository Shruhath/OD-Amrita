// Command api is the OD-Amrita HTTP server entrypoint (gin).
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
	"github.com/riverqueue/river"

	"github.com/Shruhath/OD-Amrita/api/internal/config"
	handleradmin "github.com/Shruhath/OD-Amrita/api/internal/handler/admin"
	handlerauth "github.com/Shruhath/OD-Amrita/api/internal/handler/auth"
	handlercardtoken "github.com/Shruhath/OD-Amrita/api/internal/handler/cardtoken"
	handlerdispute "github.com/Shruhath/OD-Amrita/api/internal/handler/dispute"
	handlerevent "github.com/Shruhath/OD-Amrita/api/internal/handler/event"
	"github.com/Shruhath/OD-Amrita/api/internal/handler/health"
	handlerodrecord "github.com/Shruhath/OD-Amrita/api/internal/handler/od_record"
	handlerpublicattendance "github.com/Shruhath/OD-Amrita/api/internal/handler/publicattendance"
	handlerscan "github.com/Shruhath/OD-Amrita/api/internal/handler/scan"
	handlerstudent "github.com/Shruhath/OD-Amrita/api/internal/handler/student"
	handlerteacher "github.com/Shruhath/OD-Amrita/api/internal/handler/teacher"
	"github.com/Shruhath/OD-Amrita/api/internal/job/eod_audit"
	"github.com/Shruhath/OD-Amrita/api/internal/jobs"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	corsmw "github.com/Shruhath/OD-Amrita/api/internal/middleware/cors"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/ratelimit"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	svccardtoken "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	svcdispute "github.com/Shruhath/OD-Amrita/api/internal/service/dispute"
	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	svcmarking "github.com/Shruhath/OD-Amrita/api/internal/service/marking"
	svcodrecord "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	svcpublicattendance "github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
	"github.com/Shruhath/OD-Amrita/api/internal/service/publiccache"
	svcscan "github.com/Shruhath/OD-Amrita/api/internal/service/scan"
	"github.com/Shruhath/OD-Amrita/api/internal/service/reconciliation"
	svcstudent "github.com/Shruhath/OD-Amrita/api/internal/service/student"
	svcteacher "github.com/Shruhath/OD-Amrita/api/internal/service/teacher"
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

	reconciliationSvc := reconciliation.NewService(pool)
	workerRegistry := jobs.NewRegistry()
	river.AddWorker(workerRegistry.Workers(), &eodaudit.Worker{Service: reconciliationSvc})
	// The sweep worker is registered before the client is constructed. Its
	// periodic producer is leader-elected by River, so two Fly machines do not
	// enqueue duplicate reconciliation revisions.
	riverClient, err := jobs.NewClient(pool, workerRegistry, jobs.WithPeriodicJobs(eodaudit.PeriodicSweep()))
	if err != nil {
		return err
	}
	river.AddWorker(workerRegistry.Workers(), &eodaudit.SweepWorker{Service: reconciliationSvc, Client: riverClient})
	if err := jobs.Start(context.Background(), riverClient); err != nil {
		return err
	}
	defer jobs.Stop(context.Background(), riverClient)

	if cfg.AppEnv != "development" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(errormw.Handler())
	// The router is passed to itself on purpose: the preflight's allowed-methods
	// header is read back off the routes actually registered below, so a method
	// this API serves can never go unadvertised to the browser.
	router.Use(corsmw.Middleware(cfg.PortalAllowedOrigins, router))
	router.GET("/health", health.Handler(pool))

	// The UNAUTHENTICATED public dashboard (Story 0.10). Built here, BEFORE the
	// /api/v1 group, so the two are visibly siblings in the source rather than
	// one being an easy-to-miss child of the other.
	publicLimiter, err := ratelimit.New(ratelimit.Config{
		PerMinute: cfg.PublicRateLimit.PerMinute,
		Burst:     cfg.PublicRateLimit.Burst,
		// Fly-Client-IP may be believed only when the Fly edge is actually in
		// front of this process to overwrite it. FLY_APP_NAME is set by the Fly
		// runtime and by nothing else, so it answers "am I behind the edge"
		// without inventing a secret an operator has to remember to set.
		TrustEdgeHeader: os.Getenv("FLY_APP_NAME") != "",
	})
	if err != nil {
		return err
	}
	publicDisputeLimiter, err := ratelimit.New(ratelimit.Config{
		PerMinute:       cfg.PublicDisputeRateLimit.PerMinute,
		Burst:           cfg.PublicDisputeRateLimit.Burst,
		TrustEdgeHeader: os.Getenv("FLY_APP_NAME") != "",
	})
	if err != nil {
		return err
	}
	publicGroup := newPublicGroup(router, publicLimiter.Middleware())

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

	principalLoader := rbac.NewSQLCPrincipalLoader(pool)
	cardKeyring, err := svccardtoken.NewKeyring(cfg.CardSeal.ActiveVersion, cfg.CardSeal.HMACKeys)
	if err != nil {
		return err
	}
	markAPI := api.Group("")
	markAPI.Use(rbac.RequirePrincipal(principalLoader))
	// One event reader serves both the admin surface and the coordinator marking
	// surface — the scoped reads are siblings of the admin reads, not a fork.
	eventReader := svcevent.NewSQLCReader(pool)
	creditLedger := svcodrecord.NewSQLCCreditLedger(pool)
	// The scope seam. Every marking read AND the mark write go through this one
	// policy, so a scope the reads refuse can never be reached by posting to
	// /api/v1/mark directly (NFR11). Story 0.7 replaces only its policy body.
	markingPolicy := svcmarking.NewPolicy(svcmarking.NewEventReader(eventReader))
	portalMarkReads := svcmarking.NewPortalMarkReadService(pool)
	markSvc := svcodrecord.NewService(
		svcodrecord.NewSQLCReader(pool),
		svcodrecord.NewCardTokenResolver(svccardtoken.NewSQLCLedger(pool), cardKeyring),
		svcodrecord.NewSQLCWriter(),
		st,
		nil,
		svcmarking.NewRoleAwareMarkGuard(markingPolicy, pool),
		creditLedger,
	)
	handlerodrecord.RegisterRoutes(markAPI, markSvc)
	handlerodrecord.RegisterRecordManagementRoutes(markAPI, svcodrecord.NewManagementService(pool, st))
	handlerscan.RegisterRoutes(markAPI, svcscan.NewService(markSvc, pool))
	handlerodrecord.RegisterPortalMarkRoutes(markAPI, portalMarkReads)
	// The teacher-reachable live attendance read. Mounted on markAPI rather than
	// the admin group; the service scopes a teacher to their own branches.
	handlerodrecord.RegisterPortalAttendanceRoutes(markAPI, portalMarkReads)
	handlerodrecord.RegisterPortalRecordListRoutes(markAPI, portalMarkReads)

	// One student service serves the admin roster import/list and the coordinator
	// search; the search reader is an add-on rather than a second service.
	studentSvc := svcstudent.NewService(svcstudent.NewSQLCReader(pool), st, svcstudent.NewSQLCWriter())
	studentSvc.WithSearchReader(svcstudent.NewSQLCSearchReader(pool))
	publicAttendanceSvc := svcpublicattendance.NewSQLCService(pool)
	disputeRepo := svcdispute.NewSQLCRepository(pool)
	disputeSvc := svcdispute.NewService(disputeRepo, disputeRepo, st, cfg.Auth.AllowedDomain)
	// The public reads are wrapped in the bounded in-process cache. The wrapping
	// happens HERE rather than inside the services so the services stay plain
	// database reads — the "a public read never writes" invariant is still
	// provable by reading one package — and so nothing on the authenticated
	// surface can pick up a cache by accident.
	//
	// The spike this exists for is the hour after the last teaching slot ends at
	// 18:15 IST, when the campus opens the dashboard at once. What absorbs it is
	// less the lifetimes than the collapsing of concurrent misses onto one query
	// per key; the lifetimes decide how stale a record may read, which is why the
	// records TTL is the shortest and is separately tunable.
	cacheTTL := cfg.PublicCacheTTL
	cachedRecords := publiccache.NewRecords(publicAttendanceSvc, time.Duration(cacheTTL.RecordsSeconds)*time.Second)
	cachedEvents := publiccache.NewEvents(publicAttendanceSvc, time.Duration(cacheTTL.EventsSeconds)*time.Second)
	cachedSearch := publiccache.NewSearch(studentSvc, time.Duration(cacheTTL.SearchSeconds)*time.Second)

	// The public dashboard's two reads. The SAME student service serves the
	// anonymous search — PublicSearch is a separate, actor-less, hard-capped
	// method on it, not a relaxation of the authenticated Search.
	handlerpublicattendance.RegisterPublicRoutes(publicGroup, cachedSearch, cachedRecords)
	handlerdispute.RegisterPublicRoutes(publicGroup, publicDisputeSurface{
		submitter: disputeSvc,
		events:    cachedEvents,
	}, publicDisputeLimiter.Middleware())
	handlerodrecord.RegisterMarkingReadRoutes(markAPI, svcmarking.NewReadService(
		markingPolicy,
		eventReader,
		studentSvc,
		svcodrecord.NewStatusService(svcodrecord.NewSQLCReader(pool), svcodrecord.NewSQLCCounter(pool), creditLedger, nil),
	))
	handlerdispute.RegisterRoutes(markAPI, disputeSvc)

	admin := newAdminGroup(api, principalLoader)
	admin.POST("/users/:user_id/sessions/revoke", handleradmin.RevokeSessionsHandler(revSvc))
	eventSvc := svcevent.NewService(eventReader, st, svcevent.NewSQLCWriter())
	handlerevent.RegisterAdminRoutes(admin, eventSvc)
	handlerodrecord.RegisterAdminAttendanceRoutes(admin, portalMarkReads)
	teacherSvc := svcteacher.NewService(svcteacher.NewSQLCReader(pool), st, svcteacher.NewSQLCWriter())
	handlerteacher.RegisterAdminRoutes(admin, teacherSvc)
	handlerstudent.RegisterAdminRoutes(admin, studentSvc)
	cardTokenSvc := svccardtoken.NewService(svccardtoken.NewSQLCReader(pool), svccardtoken.NewSQLCLedger(pool), st, cardKeyring)
	handlercardtoken.RegisterAdminRoutes(admin, cardTokenSvc)

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
// newAdminGroup returns the /admin group with the RBAC guard already attached.
//
// It exists as a named seam so the guard can be PROVEN attached. Mounting the
// admin routes on a group that never had RequireAdmin applied compiles cleanly —
// RegisterAdminRoutes takes a gin.IRouter, so passing `api` instead of `admin` is
// a one-token slip that silently serves every admin route to any authenticated
// caller. Nothing in the handler or rbac package tests can catch that, because
// each of them builds its own router; only the composition is at risk, so the
// composition is what gets a test (routes_test.go).
func newAdminGroup(api *gin.RouterGroup, loader rbac.PrincipalLoader) *gin.RouterGroup {
	admin := api.Group("/admin")
	admin.Use(rbac.RequireAdmin(loader))
	return admin
}

// newPublicGroup returns the UNAUTHENTICATED group the public dashboard is
// served from (Story 0.10), carrying the rate limiter and nothing else.
//
// It takes the ROOT *gin.Engine rather than the /api/v1 group, and that is the
// entire reason this function exists as a named seam. gin attaches middleware to
// the GROUP VALUE, not to the path prefix, so
//
//	router.Group("/api/v1/public")   // sibling of the auth group -> public
//	api.Group("/public")             // child  of the auth group -> 401 for everyone
//
// produce the IDENTICAL URLs and completely different middleware chains. The
// second compiles cleanly, looks right in a diff, and 401s every student on
// campus. Nothing in the handler or ratelimit tests can catch that — they build
// their own routers — so, exactly as with newAdminGroup, only the composition is
// at risk and the composition is what gets a test (public_routes_test.go).
//
// The signature takes the engine, not gin.IRouter: passing `api` would then be a
// compile error rather than a silent behaviour change.
func newPublicGroup(router *gin.Engine, limiter gin.HandlerFunc) *gin.RouterGroup {
	public := router.Group("/api/v1/public")
	public.Use(limiter)
	return public
}

func tlsMinVersion(v string) uint16 {
	if v == "1.3" {
		return tls.VersionTLS13
	}
	return tls.VersionTLS12
}

type publicDisputeSurface struct {
	submitter interface {
		Submit(context.Context, svcdispute.SubmitInput) (svcdispute.Receipt, error)
	}
	events interface {
		PublicEvents(context.Context) (svcpublicattendance.EventCollection, error)
	}
}

func (s publicDisputeSurface) Submit(ctx context.Context, input svcdispute.SubmitInput) (svcdispute.Receipt, error) {
	return s.submitter.Submit(ctx, input)
}

func (s publicDisputeSurface) PublicEvents(ctx context.Context) (svcpublicattendance.EventCollection, error) {
	return s.events.PublicEvents(ctx)
}
