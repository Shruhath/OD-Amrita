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
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Shruhath/OD-Amrita/api/internal/config"
	"github.com/Shruhath/OD-Amrita/api/internal/handler/health"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("api: %v", err)
	}

	pool, err := store.NewPool(context.Background(), cfg.DatabaseURLRuntime)
	if err != nil {
		log.Fatalf("api: %v", err)
	}
	defer pool.Close()

	if cfg.AppEnv != "development" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/health", health.Handler(pool))

	srv := &http.Server{
		Addr:    ":" + cfg.APIPort,
		Handler: router,
	}

	if cfg.TLS.Enabled {
		srv.TLSConfig = &tls.Config{MinVersion: tlsMinVersion(cfg.TLS.MinVersion)}
		log.Printf("api: serving TLS-only on %s (min TLS %s)", srv.Addr, cfg.TLS.MinVersion)
		log.Fatal(srv.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile))
	}

	// Ingress-terminated mode: the single listener, no redirect (NFR15).
	log.Printf("api: serving on %s (TLS terminated by ingress — NFR15 still applies there)", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}

// tlsMinVersion maps the configured floor to the crypto/tls constant; the
// floor is never below TLS 1.2 (NFR15).
func tlsMinVersion(v string) uint16 {
	if v == "1.3" {
		return tls.VersionTLS13
	}
	return tls.VersionTLS12
}
