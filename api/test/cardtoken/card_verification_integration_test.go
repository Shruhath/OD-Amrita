//go:build integration

// Real-Postgres tests for the card populator's DURABLE progress.
//
// THE DEFECT THIS FILE EXISTS FOR. Per-student progress lived only in the
// browser's sessionStorage. It did not survive signing out — persisted state is
// cleared deliberately, so a shared phone does not hand the next operator the
// previous one's screen — and it never crossed to a second device. An admin who
// wrote and verified a section, signed out, and came back was shown "0 written ·
// 0 verified" with every row reading "Not written", and had no way to tell which
// cards were already done except by tapping all of them again.
//
// The assertions here are against Postgres because the fix is a stored fact:
// whether the verification survives is the whole question, and a stubbed ledger
// would only prove the Go code copies the field it was handed.
package cardtokentest

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	svccardtoken "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func verificationService(t *testing.T, runtime *pgxpool.Pool) *svccardtoken.Service {
	t.Helper()
	keyring, err := svccardtoken.NewKeyring("v1", map[string][]byte{"v1": bytes.Repeat([]byte{0x42}, 32)})
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return svccardtoken.NewService(
		svccardtoken.NewSQLCReader(runtime),
		svccardtoken.NewSQLCLedger(runtime),
		store.New(runtime),
		keyring,
	)
}

func sectionOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, studentID string) svccardtoken.SectionFilter {
	t.Helper()
	var filter svccardtoken.SectionFilter
	if err := pool.QueryRow(ctx,
		`SELECT branch_id::text, batch, section FROM students WHERE id = $1`, studentID).
		Scan(&filter.BranchID, &filter.Batch, &filter.Section); err != nil {
		t.Fatalf("read student section: %v", err)
	}
	return filter
}

func statusFor(t *testing.T, statuses []svccardtoken.CardStatus, studentID string) svccardtoken.CardStatus {
	t.Helper()
	for _, status := range statuses {
		if status.StudentID == studentID {
			return status
		}
	}
	t.Fatalf("[P0] student %s is absent from the section status; a student with no card must still be counted", studentID)
	return svccardtoken.CardStatus{}
}

// CARD-VERIFY-001 — THE REPORTED DEFECT. A verification survives the session.
func TestCardVerification_SurvivesTheSessionAndIsReadBackPerStudent(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))

	studentID := uuid.NewString()
	seedStudent(t, ctx, migrator, studentID, uniqueRollNo(studentID))
	section := sectionOf(t, ctx, migrator, studentID)

	svc := verificationService(t, runtime)
	actor := svccardtoken.Actor{UserID: seedAdmin(t, ctx, migrator), Email: "admin@amrita.edu", Role: "admin"}

	// Before anything: present in the section, and plainly not done.
	before := statusFor(t, mustSectionStatus(t, ctx, svc, actor, section), studentID)
	if before.Issued || before.VerifiedAt != nil {
		t.Fatalf("[P0] an untouched student reads issued=%v verified=%v, want both empty", before.Issued, before.VerifiedAt)
	}

	sealed, err := svc.Seal(ctx, actor, svccardtoken.SealInput{StudentID: studentID})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	// ISSUED IS NOT VERIFIED. This is the distinction the whole feature turns on:
	// a token exists, but nothing has proved the card took the write, and
	// reporting it as done sends a student to the event with a blank card.
	issued := statusFor(t, mustSectionStatus(t, ctx, svc, actor, section), studentID)
	if !issued.Issued {
		t.Error("[P0] a sealed student does not read as issued")
	}
	if issued.VerifiedAt != nil {
		t.Error("[P0] sealing a card marked it VERIFIED. A seal is a token handed out; verification is a read-back " +
			"the server recognised, and collapsing them reports sections as done that are not")
	}

	if _, err := svc.Resolve(ctx, actor, svccardtoken.ResolveInput{Version: sealed.Version, Token: sealed.Token}); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// A COMPLETELY FRESH SERVICE — the sign-out the operator actually performs.
	// Nothing in this process carries state from the calls above.
	after := statusFor(t, mustSectionStatus(t, ctx, verificationService(t, runtime), actor, section), studentID)
	if after.VerifiedAt == nil {
		t.Fatal("[P0] the verification did not survive. This is the defect: an admin who verified a section, " +
			"signed out and came back was told 0 of 0 were verified, and had to tap every card again")
	}
	if time.Since(*after.VerifiedAt) > time.Hour {
		t.Errorf("[P0] verified_at = %v, which is not this run", after.VerifiedAt)
	}

	// And the ledger names WHO verified it, so the progress is auditable rather
	// than anonymous.
	var verifier *string
	if err := migrator.QueryRow(ctx,
		`SELECT verified_by_user_id::text FROM student_card_tokens WHERE student_id = $1 AND version = 'v1'`,
		studentID).Scan(&verifier); err != nil {
		t.Fatalf("read verifier: %v", err)
	}
	if verifier == nil || *verifier != actor.UserID {
		t.Errorf("[P0] verified_by_user_id = %v, want the acting admin %s", verifier, actor.UserID)
	}
}

// CARD-VERIFY-002 — a REFUSED read verifies nothing.
//
// Otherwise a mistyped or foreign card would mark the row done and the operator
// would never revisit it.
func TestCardVerification_AnUnrecognisedCardVerifiesNothing(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))

	studentID := uuid.NewString()
	seedStudent(t, ctx, migrator, studentID, uniqueRollNo(studentID))
	section := sectionOf(t, ctx, migrator, studentID)

	svc := verificationService(t, runtime)
	actor := svccardtoken.Actor{UserID: seedAdmin(t, ctx, migrator), Email: "admin@amrita.edu", Role: "admin"}
	if _, err := svc.Seal(ctx, actor, svccardtoken.SealInput{StudentID: studentID}); err != nil {
		t.Fatalf("seal: %v", err)
	}

	if _, err := svc.Resolve(ctx, actor, svccardtoken.ResolveInput{
		Version: "v1",
		Token:   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}); err == nil {
		t.Fatal("[P0] an unknown token resolved")
	}

	if got := statusFor(t, mustSectionStatus(t, ctx, svc, actor, section), studentID); got.VerifiedAt != nil {
		t.Error("[P0] a REFUSED read marked the card verified. The operator would never come back to it, and the " +
			"student arrives at the event with a card nobody checked")
	}
}

// CARD-VERIFY-003 — the section read is ADMIN ONLY.
//
// It enumerates a section's students and how far their cards have got. That is
// roster-shaped, and the populator is an admin surface.
func TestCardVerification_SectionStatusIsAdminOnly(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))

	studentID := uuid.NewString()
	seedStudent(t, ctx, migrator, studentID, uniqueRollNo(studentID))
	section := sectionOf(t, ctx, migrator, studentID)
	svc := verificationService(t, runtime)

	for _, role := range []string{"coordinator", "teacher", "student", ""} {
		t.Run("role="+role, func(t *testing.T) {
			actor := svccardtoken.Actor{UserID: uuid.NewString(), Role: role}
			if _, err := svc.SectionStatus(ctx, actor, section); err == nil {
				t.Errorf("[P0] role %q read a section's card progress", role)
			}
		})
	}
}

func mustSectionStatus(
	t *testing.T,
	ctx context.Context,
	svc *svccardtoken.Service,
	actor svccardtoken.Actor,
	filter svccardtoken.SectionFilter,
) []svccardtoken.CardStatus {
	t.Helper()
	statuses, err := svc.SectionStatus(ctx, actor, filter)
	if err != nil {
		t.Fatalf("SectionStatus: %v", err)
	}
	return statuses
}

// seedAdmin inserts the acting admin. verified_by_user_id is a real foreign key,
// so a random uuid would fail the insert rather than record the verifier.
func seedAdmin(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, name, role)
		VALUES ($1, $2, $3, 'Card Verification Admin', 'admin')`,
		id, "card-verify-"+id, "card-verify-"+id+"@amrita.edu"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	return id
}
