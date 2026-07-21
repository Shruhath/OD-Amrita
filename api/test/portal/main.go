// Command portal serves the real event HTTP stack with deterministic identities
// for the browser/Postgres acceptance job. It is test infrastructure only: the
// production API continues to verify Supabase JWTs in cmd/api.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	handlerauth "github.com/Shruhath/OD-Amrita/api/internal/handler/auth"
	handlerevent "github.com/Shruhath/OD-Amrita/api/internal/handler/event"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	corsmw "github.com/Shruhath/OD-Amrita/api/internal/middleware/cors"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	svcuser "github.com/Shruhath/OD-Amrita/api/internal/service/user"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

const (
	adminToken   = "portal-admin-token"
	studentToken = "portal-student-token"
	adminSubject = "portal-admin-subject"
	studentSub   = "portal-student-subject"
)

type fixedVerifier struct{}

func (fixedVerifier) Verify(_ context.Context, token string) (svcauth.VerifiedIdentity, error) {
	now := time.Now().UTC()
	switch token {
	case adminToken:
		return svcauth.VerifiedIdentity{Subject: adminSubject, Email: "portal-admin@amrita.edu", Name: "Portal Admin", SessionID: uuid.NewString(), IssuedAt: now}, nil
	case studentToken:
		return svcauth.VerifiedIdentity{Subject: studentSub, Email: "portal-student@amrita.edu", Name: "Portal Student", SessionID: uuid.NewString(), IssuedAt: now}, nil
	default:
		return svcauth.VerifiedIdentity{}, domain.DomainError{Code: contracts.ErrorCodeAUTHTOKENINVALID, Message: "Authentication token is invalid."}
	}
}

func main() {
	runtimeDSN := requiredEnv("DATABASE_URL_RUNTIME")
	migratorDSN := requiredEnv("DATABASE_URL_MIGRATOR")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	runtimePool := connect(ctx, runtimeDSN, "runtime")
	defer runtimePool.Close()
	migratorPool := connect(ctx, migratorDSN, "migrator")
	if err := seed(ctx, migratorPool); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			log.Fatal("seed acceptance database: timed out")
		}
		log.Fatal(fmt.Errorf("seed acceptance database: %w", err))
	}
	migratorPool.Close()

	st := store.New(runtimePool)
	userService := svcuser.NewService(st, nil, svcuser.NewSQLCProvisioner())
	auditor := svcauth.NewEventAuditor(st)
	eventService := svcevent.NewService(svcevent.NewSQLCReader(runtimePool), st, svcevent.NewSQLCWriter())

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(errormw.Handler())
	router.Use(corsmw.Middleware([]string{"http://127.0.0.1:4174"}))
	router.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	api := router.Group("/api/v1")
	api.Use(mwauth.Middleware(fixedVerifier{}, "amrita.edu", auditor))
	api.Use(mwauth.RevocationGuard(svcauth.NewSQLCRevocationChecker(runtimePool)))
	api.GET("/auth/me", handlerauth.MeHandler(userService, auditor))

	admin := api.Group("/admin")
	admin.Use(rbac.RequireAdmin(rbac.NewSQLCPrincipalLoader(runtimePool)))
	admin.GET("/branches", handlerevent.ListBranchesHandler(eventService))
	admin.GET("/events", handlerevent.ListEventsHandler(eventService))
	admin.POST("/events", handlerevent.CreateEventHandler(eventService))
	admin.POST("/events/:event_id/archive", handlerevent.ArchiveEventHandler(eventService))

	port := os.Getenv("PORTAL_API_PORT")
	if port == "" {
		port = "8081"
	}
	log.Printf("portal acceptance API listening on :%s", port)
	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatal(err)
	}
}

func requiredEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func connect(ctx context.Context, dsn, label string) *pgxpool.Pool {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect %s database: %v", label, err)
	}
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("ping %s database: %v", label, err)
	}
	return pool
}

func seed(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		DELETE FROM event_branches
		WHERE event_id IN (SELECT id FROM events WHERE name = 'Browser Persisted Event')
	`); err != nil {
		return fmt.Errorf("delete event branches: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM events WHERE name = 'Browser Persisted Event'
	`); err != nil {
		return fmt.Errorf("delete events: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, name, role)
		VALUES
			('90000000-0000-4000-8000-000000000001', $1, 'portal-admin@amrita.edu', 'Portal Admin', 'admin'),
			('90000000-0000-4000-8000-000000000002', $2, 'portal-student@amrita.edu', 'Portal Student', 'student')
		ON CONFLICT (supabase_subject) DO UPDATE
		SET email = EXCLUDED.email, name = EXCLUDED.name, role = EXCLUDED.role
	`, adminSubject, studentSub); err != nil {
		return fmt.Errorf("upsert users: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO branches (id, code, name)
		VALUES
			('91000000-0000-4000-8000-000000000001', 'CSE', 'Computer Science and Engineering'),
			('91000000-0000-4000-8000-000000000002', 'ECE', 'Electronics and Communication Engineering')
		ON CONFLICT (id) DO UPDATE SET code = EXCLUDED.code, name = EXCLUDED.name
	`); err != nil {
		return fmt.Errorf("upsert branches: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
