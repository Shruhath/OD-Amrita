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

	"github.com/Shruhath/OD-Amrita/api/internal/cachebus"
	"github.com/Shruhath/OD-Amrita/api/internal/config"
	"github.com/Shruhath/OD-Amrita/api/internal/eventstream"
	handleradmin "github.com/Shruhath/OD-Amrita/api/internal/handler/admin"
	handlerappsettings "github.com/Shruhath/OD-Amrita/api/internal/handler/appsettings"
	handlerauth "github.com/Shruhath/OD-Amrita/api/internal/handler/auth"
	handlercardtoken "github.com/Shruhath/OD-Amrita/api/internal/handler/cardtoken"
	handlerdispute "github.com/Shruhath/OD-Amrita/api/internal/handler/dispute"
	handlerevent "github.com/Shruhath/OD-Amrita/api/internal/handler/event"
	"github.com/Shruhath/OD-Amrita/api/internal/handler/health"
	handlerodrecord "github.com/Shruhath/OD-Amrita/api/internal/handler/od_record"
	handlerpublicattendance "github.com/Shruhath/OD-Amrita/api/internal/handler/publicattendance"
	handlerstudent "github.com/Shruhath/OD-Amrita/api/internal/handler/student"
	handlerteacher "github.com/Shruhath/OD-Amrita/api/internal/handler/teacher"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	corsmw "github.com/Shruhath/OD-Amrita/api/internal/middleware/cors"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/ratelimit"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcappsettings "github.com/Shruhath/OD-Amrita/api/internal/service/appsettings"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	svccardtoken "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	svcdispute "github.com/Shruhath/OD-Amrita/api/internal/service/dispute"
	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	svcmarking "github.com/Shruhath/OD-Amrita/api/internal/service/marking"
	"github.com/Shruhath/OD-Amrita/api/internal/service/markingcache"
	svcodrecord "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	svcpublicattendance "github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
	"github.com/Shruhath/OD-Amrita/api/internal/service/publiccache"
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

	// The domain event stream. OFF unless KAFKA_BROKERS is set, in which case
	// every value below is the same no-op it has always been — see package
	// eventstream. A malformed Kafka setting fails the boot here; an UNREACHABLE
	// broker deliberately does not.
	stream, err := eventstream.New(context.Background())
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stream.Close(ctx)
	}()

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
	// The BOARD's own bucket. See RegisterPublicRoutes for why it is separate
	// from the group's: campus wifi NATs every student behind one IP, so the
	// group limiter is shared by the whole campus, and the board is the one
	// endpoint the whole campus opens at once.
	publicBoardLimiter, err := ratelimit.New(ratelimit.Config{
		PerMinute:       cfg.PublicBoardRateLimit.PerMinute,
		Burst:           cfg.PublicBoardRateLimit.Burst,
		TrustEdgeHeader: os.Getenv("FLY_APP_NAME") != "",
	})
	if err != nil {
		return err
	}

	publicGroup := newPublicGroup(router, publicLimiter.Middleware())
	// Same prefix, same root router, DIFFERENT bucket. gin runs a group's
	// middleware for every route on it, so the board could not get a looser
	// limit while sharing this group — it would simply be bounded by both.
	publicBoardGroup := newPublicGroup(router, publicBoardLimiter.Middleware())

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
	//
	// WithPublisher mirrors every COMMITTED audit event onto the stream. It is the
	// only publish site in the codebase, and it covers everything: every state
	// change in this system already passes through this store's WithAudit, so the
	// stream cannot miss an event without the ledger missing it too. See
	// store/publish.go for why the publish happens after the commit and why it
	// cannot fail the write.
	st := store.New(pool, store.WithPublisher(stream.Publisher))
	// WithCurrentReader is what stops /auth/me writing on every call. Without it
	// the service keeps its original behaviour — an upsert per login — which
	// production measured as the most expensive query in the database (2,157
	// calls, 8ms each, 17s total) for a column almost nothing reads.
	userSvc := svcuser.NewService(st, cfg.Auth.BootstrapAdmins, svcuser.NewSQLCProvisioner()).
		WithCurrentReader(svcuser.NewSQLCCurrentReader(pool))

	// Auth-event auditor (Story 1.7): co-commits auth.login_succeeded (from
	// /auth/me, after provisioning) and auth.domain_rejected (from the auth
	// middleware's EnforceDomain failure branch) through the same store.WithAudit
	// boundary. One value serves both the middleware and the handler.
	eventAuditor := svcauth.NewEventAuditor(st)
	// The LOGIN audit is throttled per session; the domain-rejection audit is not.
	// They are separated here because their cadences are genuinely different: a
	// rejection is rare and every one matters, while /auth/me is called on every
	// token refresh and wrote a duplicate row each time (79% of the whole audit
	// ledger in production). The middleware keeps the unthrottled auditor.
	loginAuditor := svcauth.NewThrottledLoginAuditor(eventAuditor)

	// Forced-logout (Story 1.5c): the revocation checker the auth middleware
	// consults on every protected request, and the admin-only revocation service.
	// Supabase exposes no supported server-side target-user revoke API, so the
	// Supabase adapter is the disabled no-op — the local deny-list is authoritative.
	// Cached, because this runs on EVERY protected request and was the single
	// most-executed query in the database (15,766 calls against 319 actual
	// marks). The TTL is short — this is the forced-logout boundary, not merely
	// an authorization read — and the admin revoke path below drops the cache
	// so the machine taking the revocation never serves a stale verdict.
	revCache := svcauth.NewCachedRevocationChecker(svcauth.NewSQLCRevocationChecker(pool))
	var revChecker svcauth.RevocationChecker = revCache
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
	api.GET("/auth/me", handlerauth.MeHandler(userSvc, loginAuditor))

	// Cached for the same reason as the revocation check: this ran on every
	// authenticated request (13,571 calls) to answer a question that changes a
	// handful of times in the life of an event. See rbac.CachedPrincipalLoader
	// for what the staleness window costs.
	principalCache := rbac.NewCachedPrincipalLoader(rbac.NewSQLCPrincipalLoader(pool))
	var principalLoader rbac.PrincipalLoader = principalCache
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
	// The scope seam. Every marking read AND the mark write ask the SAME policy
	// body, so a scope the reads refuse can never be reached by posting to
	// /api/v1/mark directly (NFR11).
	//
	// TWO POLICIES OVER ONE POLICY BODY, and the split is load-bearing.
	//
	// The reads are polled — a marking screen asks all four scope questions every
	// ten seconds — so they are served from markingScopeCache. The WRITE keeps the
	// plain reader and asks the database every time. It runs a few hundred times
	// against the reads' many thousands, so caching it would save nothing and cost
	// the gate its exactness.
	//
	// What this buys is that the two can only ever disagree in ONE direction. For
	// up to the cache lifetime a read may still offer an event an administrator has
	// just archived, and the write then refuses it correctly. A write admitted on a
	// stale permission is impossible, because the write never consults the cache.
	// routes_test.go pins that, because it is a one-token slip in this file.
	plainScopeReader := svcmarking.NewEventReader(eventReader)
	markingPolicy := svcmarking.NewPolicy(plainScopeReader)
	markingScopeCache := markingcache.NewReader(
		plainScopeReader, time.Duration(cfg.MarkingScopeCacheTTLSeconds)*time.Second)
	readPolicy := svcmarking.NewPolicy(markingScopeCache)
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
	// The runtime switches. Nothing else in the graph depends on it, so it is
	// built here and handed to the three paths that judge a slot.
	settingsSvc := svcappsettings.NewService(pool)
	// The extended-slots switch reaches the three paths that judge a slot: the
	// live mark, the status countdown a coordinator watches, and corrections. If
	// any one of them read a different ceiling, a coordinator would be shown an
	// open slot that the mark path then refused.
	markSvc = markSvc.WithSlotCeiling(settingsSvc)
	handlerodrecord.RegisterRoutes(markAPI, markSvc)
	handlerodrecord.RegisterRecordManagementRoutes(
		markAPI, svcodrecord.NewManagementService(pool, st).WithSlotCeiling(settingsSvc))
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
	cachedDay := publiccache.NewDay(publicAttendanceSvc, time.Duration(cacheTTL.DaySeconds)*time.Second)

	// The public dashboard's reads. The SAME student service serves the
	// anonymous search — PublicSearch is a separate, actor-less, hard-capped
	// method on it, not a relaxation of the authenticated Search.
	handlerpublicattendance.RegisterPublicRoutes(publicGroup, publicBoardGroup, cachedSearch, cachedRecords, cachedDay)
	handlerdispute.RegisterPublicRoutes(publicGroup, publicDisputeSurface{
		submitter: disputeSvc,
		events:    cachedEvents,
	}, publicDisputeLimiter.Middleware())
	// The POLLED reads. readPolicy (cached) rather than markingPolicy (exact) —
	// see the note where the two are built.
	//
	// The status service gets its own cached event reader for the same reason and
	// with the same safety argument: it re-reads the event row on every poll, and
	// this SQLCReader instance is distinct from the one markSvc holds, so the mark
	// write's own event lookup is untouched by construction rather than by care.
	handlerodrecord.RegisterMarkingReadRoutes(markAPI, svcmarking.NewReadService(
		readPolicy,
		eventReader,
		studentSvc,
		svcodrecord.NewStatusService(
			svcodrecord.NewCachedEventReader(
				svcodrecord.NewSQLCReader(pool),
				time.Duration(cfg.MarkingScopeCacheTTLSeconds)*time.Second),
			svcodrecord.NewSQLCCounter(pool), creditLedger, nil,
		).WithSlotCeiling(settingsSvc),
	))
	handlerdispute.RegisterRoutes(markAPI, disputeSvc)

	admin := newAdminGroup(api, principalLoader)
	handlerappsettings.RegisterAdminRoutes(admin, settingsSvc)
	// Wrapped so a successful revoke drops the cached verdicts on THIS machine
	// immediately, rather than leaving the administrator to watch a revoked
	// session keep working for the length of the cache window. Peer machines
	// expire on their own inside RevocationCacheTTL — the same shape the public
	// read cache already has.
	// The two revocation caches join the invalidation bus. What changes is the
	// REACH of a clear, not its meaning: revokeDroppingCaches still calls Clear()
	// on a value it knows nothing else about, but that value now drops the entry
	// on every machine instead of only this one. With no broker configured the
	// bus clears locally and publishes to events.Nop, which is byte-for-byte the
	// behaviour this line had before the bus existed.
	stream.Cache.Register(cachebus.ScopeRevocation, revCache)
	stream.Cache.Register(cachebus.ScopePrincipal, principalCache)
	admin.POST("/users/:user_id/sessions/revoke",
		handleradmin.RevokeSessionsHandler(revokeDroppingCaches{
			inner:      revSvc,
			revocation: stream.Cache.Clearing("session revoked", cachebus.ScopeRevocation),
			principals: stream.Cache.Clearing("session revoked", cachebus.ScopePrincipal),
		}))
	eventSvc := svcevent.NewService(eventReader, st, svcevent.NewSQLCWriter())
	// Wrapped for the same reason the revoke handler is: an administrator who
	// archives an event or moves an assignment should not then watch the marking
	// screen keep offering the old answer for a minute. Peer machines expire on
	// their own inside the cache lifetime.
	stream.Cache.Register(cachebus.ScopeMarkingScope, markingScopeCache)
	handlerevent.RegisterAdminRoutes(admin, eventEditsDroppingScopeCache{
		Service: eventSvc,
		// Fleet-wide now, for the reason given at the revoke handler above. This
		// is the case an administrator actually notices: archive an event, and
		// before the bus the OTHER machine kept offering it to coordinators for
		// the rest of the cache window — so whether the change appeared depended
		// on which machine a poll happened to land on.
		scope: stream.Cache.Clearing("event scope edited", cachebus.ScopeMarkingScope),
	})
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

// revokeDroppingCaches performs a revocation and then invalidates the caches
// that would otherwise keep answering from before it.
//
// It lives here rather than inside either cache because neither of them should
// know what a revocation is: the caches are general, and the ORDER — write
// first, invalidate second, invalidate only on success — is a property of this
// composition. Invalidating first would leave a window where a reload could
// re-cache the pre-revocation answer; invalidating on failure would throw away
// good entries for nothing.
type revokeDroppingCaches struct {
	inner interface {
		RevokeUserSessions(ctx context.Context, actor svcauth.VerifiedIdentity, targetUserID string, req svcauth.RevokeRequest) (svcauth.Revocation, error)
	}
	revocation interface{ Clear() }
	principals interface{ Clear() }
}

func (r revokeDroppingCaches) RevokeUserSessions(
	ctx context.Context,
	actor svcauth.VerifiedIdentity,
	targetUserID string,
	req svcauth.RevokeRequest,
) (svcauth.Revocation, error) {
	revocation, err := r.inner.RevokeUserSessions(ctx, actor, targetUserID, req)
	if err != nil {
		return revocation, err
	}
	// Both caches are cleared ENTIRELY rather than by key, and that is deliberate
	// rather than lazy. The service's result reports the row it wrote, not the
	// subject it wrote it for, and the revocation cache's keys carry a session id
	// and an issue time that cannot be reconstructed from a subject anyway.
	//
	// The cost is one reload per active caller, once, at the moment an
	// administrator deliberately forced somebody out — which is rare, and is
	// exactly the moment to prefer certainty over cleverness.
	r.revocation.Clear()
	r.principals.Clear()
	return revocation, nil
}

// eventEditsDroppingScopeCache is the event admin service, with every mutation
// followed by a drop of this machine's cached marking scope.
//
// It EMBEDS the service rather than listing all fifteen methods, so the reads and
// the credit endpoints pass straight through and a method added to the service
// later is carried automatically instead of failing to compile here. Only the six
// calls that can change what a coordinator may mark are overridden.
//
// The order matches revokeDroppingCaches for the same reasons: write first, drop
// second, and only on success. Dropping first would leave a window in which a
// concurrent poll re-cached the pre-edit answer.
type eventEditsDroppingScopeCache struct {
	*svcevent.Service
	scope interface{ Clear() }
}

func (e eventEditsDroppingScopeCache) Create(ctx context.Context, actor svcevent.Actor, input svcevent.CreateInput) (svcevent.Event, error) {
	return dropScopeAfter(e.scope, func() (svcevent.Event, error) { return e.Service.Create(ctx, actor, input) })
}

func (e eventEditsDroppingScopeCache) Archive(ctx context.Context, actor svcevent.Actor, eventID string) (svcevent.Event, error) {
	return dropScopeAfter(e.scope, func() (svcevent.Event, error) { return e.Service.Archive(ctx, actor, eventID) })
}

func (e eventEditsDroppingScopeCache) CreateCategory(ctx context.Context, actor svcevent.Actor, input svcevent.CreateCategoryInput) (svcevent.Category, error) {
	return dropScopeAfter(e.scope, func() (svcevent.Category, error) { return e.Service.CreateCategory(ctx, actor, input) })
}

func (e eventEditsDroppingScopeCache) UpdateCategoryVisibility(ctx context.Context, actor svcevent.Actor, input svcevent.UpdateCategoryVisibilityInput) (svcevent.Category, error) {
	return dropScopeAfter(e.scope, func() (svcevent.Category, error) {
		return e.Service.UpdateCategoryVisibility(ctx, actor, input)
	})
}

func (e eventEditsDroppingScopeCache) CreateCoordinatorAssignment(ctx context.Context, actor svcevent.Actor, input svcevent.CreateCoordinatorAssignmentInput) (svcevent.CoordinatorAssignment, error) {
	return dropScopeAfter(e.scope, func() (svcevent.CoordinatorAssignment, error) {
		return e.Service.CreateCoordinatorAssignment(ctx, actor, input)
	})
}

func (e eventEditsDroppingScopeCache) UpdateCoordinatorCategoryAccess(ctx context.Context, actor svcevent.Actor, input svcevent.UpdateCoordinatorCategoryAccessInput) (svcevent.CoordinatorAssignment, error) {
	return dropScopeAfter(e.scope, func() (svcevent.CoordinatorAssignment, error) {
		return e.Service.UpdateCoordinatorCategoryAccess(ctx, actor, input)
	})
}

// dropScopeAfter runs the write and clears the cache only when it SUCCEEDED.
//
// A closure rather than a (value, error) pair because Go cannot spread a
// multi-value call alongside another argument — and it lands better anyway: the
// rule that a failed edit must not throw away good entries lives in exactly one
// place instead of being restated in six.
func dropScopeAfter[T any](scope interface{ Clear() }, write func() (T, error)) (T, error) {
	value, err := write()
	if err == nil {
		scope.Clear()
	}
	return value, err
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
