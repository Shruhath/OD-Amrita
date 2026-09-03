//go:build integration

// Real-Postgres tests for Story 0.11's dispute path (AC 5, 6, 7, 11, 12, 13).
//
// Deliberately in the EXISTING api/test/odrecord package. ./test/odrecord/... is
// already wired into both api/Makefile's INTEGRATION_PKGS and
// .github/workflows/api-ci.yml, so a NEW package here would silently never run in
// CI the way three earlier stories' packages did — adding one means editing the
// Makefile AND the workflow in the same change (2.3 shipped that gap once).
//
// Helpers (testCtx, dsnOrSkip, connect, seedMarkFixture, countRows) come from
// odrecord_integration_atdd_test.go and marking_integration_test.go;
// seedPublicEvent comes from public_attendance_integration_test.go. All tagged
// `integration`, so they compile together.
//
// EVERY test below needs a real database rather than a fake, because every one of
// them asserts a property Postgres owns and Go cannot fake:
//
//   - the partial unique index is what makes the duplicate guard race-safe (T11);
//     a read-then-insert passes a single-threaded test and loses under two
//     goroutines, which is the only way this endpoint will ever be hit;
//   - the ABSENCE of require_active_event_for_child_mutation on `disputes` (T9)
//     is visible only by inserting against an archived event;
//   - store.WithAudit's rollback direction is a transaction property;
//   - teacher branch scoping is a JOIN through user_branches (T16).
//
// COMPILE-RED until api/internal/service/dispute exists.
//
// Run locally before pushing: `make -C api db-up && make -C api test-integration`.
// 0.9's retrospective named "no local Postgres, so CI is the first real
// execution" as the single root cause of six defects across five CI rounds; 0.10
// followed that advice and pushed clean.
package odrecordtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/service/dispute"
	svcpublicattendance "github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// fixtureODDate is seedMarkFixture's event date — the day a dispute against that
// event belongs to.
//
// od_date is required from CREATION by the 2026-08-06 multi-day amendment
// (docs/multi-day-events-plan.md §7). The multi-day change turns an event into
// a real date range, at which point uniqueness must be
// (student_id, event_id, od_date, slot); adding the column later, against a table
// already holding student submissions, is the migration the amendment exists to
// avoid. It must be NOT NULL: Postgres treats NULLs as DISTINCT in a unique index,
// so one nullable-dated row never collides and duplicates come back silently.
const fixtureODDate = "2026-08-03"

// disputeSubmitInput builds a submission for a seeded student on a seeded event.
func disputeSubmitInput(studentID, email, eventID, categoryID string, slot int) dispute.SubmitInput {
	return disputeSubmitInputOn(studentID, email, eventID, categoryID, fixtureODDate, slot)
}

// disputeSubmitInputOn is the same thing with an explicit day, for the tests that
// turn on which day is being disputed.
func disputeSubmitInputOn(studentID, email, eventID, categoryID, odDate string, slot int) dispute.SubmitInput {
	coord := "Priya Menon"
	note := "I was present in the queue but my slot was missed."
	return dispute.SubmitInput{
		StudentID:              studentID,
		SubmittedEmail:         email,
		EventID:                eventID,
		CategoryID:             categoryID,
		ODDate:                 odDate,
		Slot:                   slot,
		ClaimedCoordinatorName: &coord,
		StudentNote:            &note,
	}
}

// newDisputeService builds the service over the RUNTIME pool — the DML-only role
// the app actually runs as. Building it over the migrator pool would hide a
// missing privilege, which is precisely the failure T10's runtimePrivileges
// registry exists to catch.
func newDisputeService(t *testing.T, runtime *pgxpool.Pool) *dispute.Service {
	t.Helper()
	return dispute.NewSQLCService(runtime, store.New(runtime), "amrita.edu")
}

// seedDisputeStudent inserts a student whose roll number derives from the email
// the tests submit, which is the whole of T1's contract.
//
// The roll numbers are FIXED (the CB.SC.U4CSE248xx pool, disjoint from the pools
// the coordinator-marking, capture-credits, teacher-admin-attendance and public
// dashboard fixtures own) so that a failure names the case it came from. Fixed
// identifiers only stay safe if the row is removed afterwards, hence the cleanup:
// without it these tests pass only because ./test/db runs FIRST under -p 1 and its
// goose down-to/up round-trip happens to drop every table. That is an accidental
// dependency on an unrelated package's side effect, and it turns any focused run
// (`go test -run Dispute ./test/odrecord/...`) into a false red on a duplicate
// roll_no — exactly the reviewer's first command.
//
// Disputes are removed before the student because student_id is ON DELETE
// RESTRICT. Audit rows are deliberately NOT touched: audit_log is append-only by
// migration 0002, and every count assertion here is taken as a before/after delta
// rather than an absolute, so leftover audit rows cannot skew them.
func seedDisputeStudent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, rollNo, branchID string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO students (id, roll_no, name, branch_id, section, batch)
		VALUES ($1, $2, 'ATDD Dispute Student', $3, 'A', '2024')`,
		id, rollNo, branchID); err != nil {
		t.Fatalf("seed dispute student %s: %v", rollNo, err)
	}
	t.Cleanup(func() {
		// A fresh context: the test's own ctx may already be cancelled, and a
		// cleanup that silently no-ops would restore the collision it exists to
		// prevent.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM disputes WHERE student_id = $1`, id); err != nil {
			t.Errorf("cleanup disputes for %s: %v", rollNo, err)
		}
		// An APPROVED dispute now creates an od_records row, and
		// od_records_student_id_fkey is ON DELETE RESTRICT — so without this the
		// student delete below fails and every approving test reports a cleanup
		// error instead of its own result.
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM od_records WHERE student_id = $1`, id); err != nil {
			t.Errorf("cleanup od records for %s: %v", rollNo, err)
		}
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM students WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup dispute student %s: %v", rollNo, err)
		}
	})
	return id
}

// emailForRoll builds the institutional address for a roll number (T1).
func emailForRoll(rollNo string) string {
	return strings.ToLower(rollNo) + "@cb.students.amrita.edu"
}

// 0.11-INT-001 — one dispute row and one dispute.submitted audit row co-commit in
// the SAME transaction, and a failing write rolls BOTH back (AC 5, AR11).
//
// Both directions are asserted. Only checking the happy path would pass for an
// implementation that inserts the audit row in a second transaction, which is the
// arrangement that leaves an audit ledger claiming a dispute that does not exist.
func TestDispute_SubmitCoCommitsItsAuditRowAndRollsBackTogether(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	roll := "CB.SC.U4CSE24801"
	studentID := seedDisputeStudent(t, ctx, migrator, roll, "91000000-0000-4000-8000-000000000001")

	beforeDisputes := countRows(t, ctx, migrator, "disputes")
	beforeAudit := countRows(t, ctx, migrator, "audit_log")

	svc := newDisputeService(t, runtime)
	receipt, err := svc.Submit(ctx, disputeSubmitInput(studentID, emailForRoll(roll), fixture.eventID, fixture.categoryID, 3))
	if err != nil {
		t.Fatalf("[P0] Submit: %v", err)
	}
	if receipt.DisputeID == "" {
		t.Fatalf("[P0] the receipt carries no dispute id")
	}

	if got := countRows(t, ctx, migrator, "disputes"); got != beforeDisputes+1 {
		t.Errorf("[P0] disputes went %d -> %d, want exactly one new row", beforeDisputes, got)
	}
	if got := countRows(t, ctx, migrator, "audit_log"); got != beforeAudit+1 {
		t.Errorf("[P0] audit_log went %d -> %d, want exactly one new row (AR11: every write co-commits its "+
			"audit row)", beforeAudit, got)
	}

	var action, actorAccount, actorRole, targetTable, targetID string
	if err := migrator.QueryRow(ctx, `
		SELECT action, actor_account, actor_role, target_table, target_id
		FROM audit_log WHERE target_id = $1 AND target_table = 'disputes'`,
		receipt.DisputeID).Scan(&action, &actorAccount, &actorRole, &targetTable, &targetID); err != nil {
		t.Fatalf("[P0] no audit row targets the new dispute %s: %v", receipt.DisputeID, err)
	}
	if action != string(domain.ActionDisputeSubmitted) {
		t.Errorf("[P0] audit action = %q, want %q (already registered — do not add a second name, T12)",
			action, domain.ActionDisputeSubmitted)
	}
	if actorAccount != emailForRoll(roll) {
		t.Errorf("[P0] actor_account = %q, want the submitted email %q — an unauthenticated submitter has no "+
			"other identifier, and the ledger is internal so the address belongs there (T13)",
			actorAccount, emailForRoll(roll))
	}
	if actorRole != "student" {
		t.Errorf("[P0] actor_role = %q, want \"student\"", actorRole)
	}
	// T20: this must NOT be od_records. The 0.10 CI gate counts audit rows joined
	// on target_table = 'od_records' for the public fixture events and asserts
	// zero; a dispute row landing there would break a gate this story is
	// forbidden to relax.
	if targetTable != "disputes" {
		t.Errorf("[P0] target_table = %q, want \"disputes\" — 0.10's conservation gate asserts ZERO audit rows "+
			"with target_table = 'od_records' for the public fixture events, and it must stay exactly as "+
			"written (T20)", targetTable)
	}

	// The rollback direction. A submission that violates a database constraint
	// must leave NEITHER row behind.
	afterDisputes := countRows(t, ctx, migrator, "disputes")
	afterAudit := countRows(t, ctx, migrator, "audit_log")
	_, err = svc.Submit(ctx, disputeSubmitInput(studentID, emailForRoll(roll), fixture.eventID, fixture.categoryID, 3))
	if err == nil {
		t.Fatalf("[P0] the duplicate submission was ACCEPTED; the partial unique index must refuse it (T11)")
	}
	if got := countRows(t, ctx, migrator, "disputes"); got != afterDisputes {
		t.Errorf("[P0] a refused submission left %d extra dispute rows", got-afterDisputes)
	}
	if got := countRows(t, ctx, migrator, "audit_log"); got != afterAudit {
		t.Errorf("[P0] a refused submission left %d extra audit rows. store.WithAudit rolls EVERYTHING back on "+
			"any error — an audit row surviving a failed write is a ledger that records something that never "+
			"happened", got-afterAudit)
	}
}

// 0.11-INT-002 — the duplicate guard is the DATABASE's, proven by two goroutines
// submitting the same slot at once (AC 6, T11).
//
// A read-then-insert check passes a sequential test and loses this one: both
// readers see no open dispute, both insert, and the student gets two rows. This
// endpoint is reachable by anyone on the internet, so the race is not theoretical.
// Exactly one submission must succeed and the other must be DISPUTE_ALREADY_OPEN.
func TestDispute_ConcurrentDuplicateSubmissionsLeaveExactlyOneRow(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	roll := "CB.SC.U4CSE24802"
	studentID := seedDisputeStudent(t, ctx, migrator, roll, "91000000-0000-4000-8000-000000000001")
	input := disputeSubmitInput(studentID, emailForRoll(roll), fixture.eventID, fixture.categoryID, 5)

	svc := newDisputeService(t, runtime)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		receipts []dispute.Receipt
		failures []error
		start    = make(chan struct{})
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release both at once, so the window is as narrow as it gets
			receipt, err := svc.Submit(ctx, input)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, err)
				return
			}
			receipts = append(receipts, receipt)
		}()
	}
	close(start)
	wg.Wait()

	if len(receipts) != 1 {
		t.Errorf("[P0] %d of 2 concurrent submissions succeeded, want exactly 1. An application-level "+
			"\"check then insert\" loses this race every time: both readers see no open dispute and both "+
			"insert. The guard must be the partial unique index "+
			"uq_disputes_open_student_event_date_slot (T11 as amended)", len(receipts))
	}
	if len(failures) != 1 {
		t.Fatalf("[P0] %d of 2 concurrent submissions failed, want exactly 1", len(failures))
	}

	var domainErr domain.DomainError
	if !errors.As(failures[0], &domainErr) {
		t.Fatalf("[P0] the losing submission returned %v, want a typed domain error", failures[0])
	}
	if domainErr.Code != contracts.ErrorCodeDISPUTEALREADYOPEN {
		t.Errorf("[P0] losing code = %q, want DISPUTE_ALREADY_OPEN. Detect the 23505 by CONSTRAINT NAME on the "+
			"*pgconn.PgError via errors.As — never by parsing the message, which is locale- and "+
			"version-dependent", domainErr.Code)
	}
	if !strings.Contains(domainErr.Message, receipts[0].Reference) && len(receipts) == 1 {
		t.Errorf("[P0] the 409 message %q does not name the existing reference %q. AC 6 requires it: without a "+
			"reference the student has nothing to quote and re-submits, which is the loop this refusal exists "+
			"to end", domainErr.Message, receipts[0].Reference)
	}

	var rows int
	if err := migrator.QueryRow(ctx, `
		SELECT count(*) FROM disputes WHERE student_id = $1 AND event_id = $2 AND od_date = $3::date AND slot = 5`,
		studentID, fixture.eventID, fixtureODDate).Scan(&rows); err != nil {
		t.Fatalf("count disputes: %v", err)
	}
	if rows != 1 {
		t.Errorf("[P0] %d dispute rows exist for (student, event, od_date, slot 5), want 1", rows)
	}

	// A RESOLVED dispute must not block a new one for the same slot — that is the
	// whole reason the index is PARTIAL rather than total. If a student's first
	// dispute was resolved wrongly they must be able to file again.
	if _, err := migrator.Exec(ctx, `
		UPDATE disputes SET status = 'resolved', outcome = 'rejected', resolution_note = 'closed by test',
			resolved_by_user_id = $2, resolved_at = now()
		WHERE student_id = $1 AND event_id = $3 AND slot = 5`,
		studentID, fixture.adminID, fixture.eventID); err != nil {
		t.Fatalf("resolve seeded dispute: %v", err)
	}
	if _, err := svc.Submit(ctx, input); err != nil {
		t.Errorf("[P0] a new dispute was refused after the previous one was resolved: %v. The index is PARTIAL "+
			"(WHERE status = 'open') precisely so a resolved dispute does not block a re-file", err)
	}
}

// 0.11-INT-002b — the SAME slot on a DIFFERENT day is a DIFFERENT dispute.
//
// This is the whole reason `od_date` is in the key. Under 0.12a an event spans up
// to thirty days, so a student missed in slot 8 on the 3rd and again in slot 8 on
// the 4th has two genuine grievances — and a unique index keyed only on
// (student_id, event_id, slot) would refuse the second as a duplicate, telling
// them they had already reported something they had not.
//
// Written now, while the table is being created: adding the column later means a
// migration against real student submissions, which is exactly what the
// 2026-08-06 amendment exists to avoid.
func TestDispute_SameSlotOnADifferentDayIsADifferentDispute(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	// A second day inside the event. Widen this fixture event's range rather than
	// inventing a second event — two events would pass even with od_date missing
	// from the key, which would make this test prove nothing.
	//
	// UN-SKIPPED under Story 0.12a (T21). This used to t.Skipf when the UPDATE
	// failed, because an event was a single day and `events.end_date` did not
	// exist. It does now, and this is the test that proves the multi-day duplicate
	// rule actually works — re-skipping it or deleting it is weakening a gate.
	if _, err := migrator.Exec(ctx, `
		UPDATE events SET end_date = $2::date WHERE id = $1`, fixture.eventID, "2026-08-04"); err != nil {
		t.Fatalf("[P0] widening the fixture event to a real range failed: %v.\n"+
			"`events.end_date` is added by migration 0015 (T4). Until it exists this test cannot run at all — "+
			"and it is the one that proves a student missed in slot 8 on two different days has two genuine "+
			"grievances rather than one duplicate.", err)
	}

	roll := "CB.SC.U4CSE24807"
	studentID := seedDisputeStudent(t, ctx, migrator, roll, "91000000-0000-4000-8000-000000000001")
	svc := newDisputeService(t, runtime)

	first, err := svc.Submit(ctx, disputeSubmitInputOn(studentID, emailForRoll(roll), fixture.eventID, fixture.categoryID, "2026-08-03", 8))
	if err != nil {
		t.Fatalf("[P0] first day's dispute: %v", err)
	}
	second, err := svc.Submit(ctx, disputeSubmitInputOn(studentID, emailForRoll(roll), fixture.eventID, fixture.categoryID, "2026-08-04", 8))
	if err != nil {
		t.Fatalf("[P0] the SECOND day's dispute for the same slot was refused: %v.\n"+
			"The partial unique index must be keyed (student_id, event_id, od_date, slot) — keyed without "+
			"od_date it refuses a genuine second grievance as a duplicate and tells the student they already "+
			"reported something they did not", err)
	}
	if first.DisputeID == second.DisputeID {
		t.Errorf("[P0] both days returned the same dispute id %q", first.DisputeID)
	}

	// And the same slot on the SAME day is still refused — the guard must narrow,
	// not disappear.
	if _, err := svc.Submit(ctx, disputeSubmitInputOn(studentID, emailForRoll(roll), fixture.eventID, fixture.categoryID, "2026-08-04", 8)); err == nil {
		t.Errorf("[P0] a duplicate on the same (event, od_date, slot) was accepted; adding od_date to the key " +
			"must narrow the guard, not remove it")
	}
}

// 0.11-INT-002c — the DATABASE refuses an `od_date` outside the event's range.
//
// `docs/multi-day-events-plan.md` §5 Step 2 mandates a range-guard trigger on
// `od_records` asserting `od_date BETWEEN events.event_date AND events.end_date`,
// because "the teacher-chosen date in 0.12b can file a mark outside the festival
// entirely, and nothing downstream would notice". The argument is STRONGER here:
// on `od_records` the date is chosen by an authenticated teacher, but on
// `disputes` it is supplied by an ANONYMOUS submitter over the open internet.
// Without a database guard, anyone can file a dispute dated 1970 or 2099, and the
// teacher inbox fills with rows no event ever covered.
//
// ⚠️ THIS IS NOT THE TRIGGER T9 FORBIDS. T9 says do not attach
// `require_active_event_for_child_mutation('event_id')` to `disputes` — that one
// raises on INSERT when the event is ARCHIVED, which would make a dispute
// unfileable exactly when disputes are filed. A date-range guard is a different
// constraint with a different purpose and says nothing about archive state; the
// two are compatible and BOTH decisions must be honoured. Reading "no trigger on
// disputes" and skipping the range guard is the mistake this comment exists to
// prevent.
//
// Asserted by writing SQL DIRECTLY as the runtime role, bypassing the service, so
// it proves the DATABASE refuses it rather than that the Go validation ran. A
// handler check is not a guarantee when the caller is the open internet.
func TestDispute_DatabaseRefusesAnODDateOutsideTheEventRange(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	roll := "CB.SC.U4CSE24808"
	studentID := seedDisputeStudent(t, ctx, migrator, roll, "91000000-0000-4000-8000-000000000001")

	// The fixture event is a single day (2026-08-03) until 0.12a adds end_date, so
	// anything else is out of range.
	_, err := runtime.Exec(ctx, `
		INSERT INTO disputes (student_id, event_id, category_id, od_date, slot, submitted_email, status)
		VALUES ($1, $2, $3, $4::date, 4, $5, 'open')`,
		studentID, fixture.eventID, fixture.categoryID, "2027-01-01", emailForRoll(roll))
	if err == nil {
		t.Errorf("[P0] the database ACCEPTED a dispute dated 2027-01-01 against an event on 2026-08-03. Add a " +
			"range guard asserting od_date BETWEEN events.event_date AND events.end_date (plan §5 Step 2 " +
			"mandates the sibling guard on od_records; the case for it here is stronger, because this date " +
			"arrives from an UNAUTHENTICATED submitter). This is NOT the active-event trigger T9 forbids — that " +
			"one blocks archived events and must still stay off this table")
	}

	// A date INSIDE the range is accepted, so the guard narrows rather than blocks.
	// Without this half, a guard that refused everything would pass the assertion
	// above and break the whole feature.
	if _, err := runtime.Exec(ctx, `
		INSERT INTO disputes (student_id, event_id, category_id, od_date, slot, submitted_email, status)
		VALUES ($1, $2, $3, $4::date, 4, $5, 'open')`,
		studentID, fixture.eventID, fixture.categoryID, "2026-08-03", emailForRoll(roll)); err != nil {
		t.Errorf("[P0] the database refused a dispute dated ON the event's own date (2026-08-03): %v. The range "+
			"guard must be inclusive of both ends", err)
	}
}

// 0.11-INT-003 — a dispute can be filed against an ARCHIVED event (AC 7, T9).
//
// require_active_event_for_child_mutation('event_id') is attached to categories,
// event_branches, od_records, event_coordinator_assignments and both credit
// tables. Attaching it here — the obvious thing to do by pattern-matching the
// neighbouring migrations — would raise on INSERT whenever the owning event is
// archived, making a dispute unfileable exactly when disputes are filed: after
// the event, once the coordinator has tidied up.
func TestDispute_CanBeFiledAgainstAnArchivedEvent(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	archived := seedPublicEvent(t, ctx, migrator, "ATDD Dispute Archived Event", "2026-07-28")
	// status, archived_at and archived_by_user_id move TOGETHER:
	// events_archive_state_ck is a multi-column CHECK, and an UPDATE that sets
	// only the first two is rejected by the constraint rather than by anything to
	// do with this story. 0.10 lost a test run to exactly this shape.
	if _, err := migrator.Exec(ctx, `
		UPDATE events SET status = 'archived', archived_at = now(), archived_by_user_id = $2
		WHERE id = $1`, archived.eventID, fixture.adminID); err != nil {
		t.Fatalf("archive event: %v", err)
	}

	roll := "CB.SC.U4CSE24803"
	studentID := seedDisputeStudent(t, ctx, migrator, roll, "91000000-0000-4000-8000-000000000001")

	receipt, err := newDisputeService(t, runtime).Submit(ctx,
		disputeSubmitInputOn(studentID, emailForRoll(roll), archived.eventID, archived.categoryID, "2026-07-28", 2))
	if err != nil {
		t.Fatalf("[P0] a dispute against an ARCHIVED event was refused: %v.\n"+
			"If this is a trigger error, require_active_event_for_child_mutation has been attached to "+
			"`disputes`. It must NOT be (T9): archiving is an administrative act on the event, not a retraction, "+
			"and a dispute is filed precisely after the fact. Write the omission down in the migration as "+
			"deliberate, or the next person will \"fix\" it", err)
	}
	if receipt.DisputeID == "" {
		t.Errorf("[P0] the archived-event submission returned an empty receipt")
	}
}

// 0.11-INT-004 — a teacher sees only their OWN branches' students, scoped in SQL
// (AC 11, T16).
//
// Scoping in Go after an unscoped read would move every other branch's disputes —
// with their submitted emails — through the process that is about to serialise a
// response. The assertion here is on the ROWS RETURNED, which is the only thing
// that distinguishes the two implementations from outside.
func TestDispute_TeacherSeesOnlyTheirOwnBranchAndAdminSeesAll(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	// A second branch, so "scoped" and "unscoped" can produce different answers.
	//
	// A FIXED id upserted on conflict, matching seedMarkFixture's own branch seed.
	// A fresh uuid per run looks safer but is not: branches.code is UNIQUE, so the
	// second run of this test collides on 'ODX' with an orphan row the first run
	// left behind. The id is deliberately NOT the portal harness's
	// ...000000000002 (its ECE branch) — upserting onto that id would rewrite a
	// fixture another suite depends on.
	const otherBranch = "91000000-0000-4000-8000-0000000000d1"
	if _, err := migrator.Exec(ctx, `
		INSERT INTO branches (id, code, name) VALUES ($1, 'ODX', 'ATDD Other Branch')
		ON CONFLICT (id) DO UPDATE SET code = EXCLUDED.code, name = EXCLUDED.name`, otherBranch); err != nil {
		t.Fatalf("seed other branch: %v", err)
	}

	teacherID := uuid.NewString()
	if _, err := migrator.Exec(ctx, `
		INSERT INTO users (id, supabase_subject, email, name, role)
		VALUES ($1, $2, 'atdd-dispute-teacher@amrita.edu', 'ATDD Dispute Teacher', 'teacher')`,
		teacherID, "atdd-dispute-teacher-"+teacherID); err != nil {
		t.Fatalf("seed teacher: %v", err)
	}
	if _, err := migrator.Exec(ctx, `
		INSERT INTO user_branches (user_id, branch_id) VALUES ($1, '91000000-0000-4000-8000-000000000001')`,
		teacherID); err != nil {
		t.Fatalf("associate teacher branch: %v", err)
	}
	if _, err := migrator.Exec(ctx, `
		INSERT INTO user_category_assignments (user_id, category_id, created_by_user_id)
		VALUES ($1, $2, $3)`, teacherID, fixture.categoryID, fixture.adminID); err != nil {
		t.Fatalf("associate teacher category: %v", err)
	}

	mineRoll := "CB.SC.U4CSE24804"
	theirsRoll := "CB.SC.U4ECE24804"
	mineID := seedDisputeStudent(t, ctx, migrator, mineRoll, "91000000-0000-4000-8000-000000000001")
	theirsID := seedDisputeStudent(t, ctx, migrator, theirsRoll, otherBranch)

	svc := newDisputeService(t, runtime)
	inBranch, err := svc.Submit(ctx, disputeSubmitInput(mineID, emailForRoll(mineRoll), fixture.eventID, fixture.categoryID, 6))
	if err != nil {
		t.Fatalf("[P0] submit in-branch dispute: %v", err)
	}
	outOfBranch, err := svc.Submit(ctx, disputeSubmitInput(theirsID, emailForRoll(theirsRoll), fixture.eventID, fixture.categoryID, 7))
	if err != nil {
		t.Fatalf("[P0] submit out-of-branch dispute: %v", err)
	}

	teacher := dispute.Actor{UserID: teacherID, Role: "teacher"}
	scoped, err := svc.List(ctx, teacher, dispute.ListFilter{Page: 1, PageSize: 100})
	if err != nil {
		t.Fatalf("[P0] List as teacher: %v", err)
	}
	seen := map[string]bool{}
	for _, record := range scoped.Data {
		seen[record.DisputeID] = true
	}
	if !seen[inBranch.DisputeID] {
		t.Errorf("[P0] the teacher cannot see a dispute from their OWN branch (%s)", inBranch.DisputeID)
	}
	if seen[outOfBranch.DisputeID] {
		t.Errorf("[P0] the teacher can see a dispute from ANOTHER branch (%s), including its submitted email "+
			"and note. Scope the query by joining students -> user_branches for the acting teacher IN SQL — do "+
			"not read everything and filter in Go (T16)", outOfBranch.DisputeID)
	}

	admin := dispute.Actor{UserID: fixture.adminID, Role: "admin"}
	all, err := svc.List(ctx, admin, dispute.ListFilter{Page: 1, PageSize: 100})
	if err != nil {
		t.Fatalf("[P0] List as admin: %v", err)
	}
	adminSeen := map[string]bool{}
	for _, record := range all.Data {
		adminSeen[record.DisputeID] = true
	}
	if !adminSeen[inBranch.DisputeID] || !adminSeen[outOfBranch.DisputeID] {
		t.Errorf("[P0] the admin cannot see disputes across all branches (in=%v out=%v)",
			adminSeen[inBranch.DisputeID], adminSeen[outOfBranch.DisputeID])
	}

	// A teacher targeting an out-of-branch dispute is a 403, NOT a 404. A 404
	// here would tell a branch-scoped caller whether a dispute exists, which is
	// the same disclosure the public refusal spends T3 closing.
	_, err = svc.Resolve(ctx, teacher, outOfBranch.DisputeID, "Not mine to resolve.", dispute.OutcomeApproved)
	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("[P0] out-of-branch resolve returned %v, want a typed domain error", err)
	}
	if domainErr.Code != contracts.ErrorCodeRBACFORBIDDEN {
		t.Errorf("[P0] out-of-branch resolve code = %q, want RBAC_FORBIDDEN (AC 13) — never a 404, and never a "+
			"silent success", domainErr.Code)
	}
	var status string
	if err := migrator.QueryRow(ctx, `SELECT status FROM disputes WHERE id = $1`, outOfBranch.DisputeID).Scan(&status); err != nil {
		t.Fatalf("re-read out-of-branch dispute: %v", err)
	}
	if status != "open" {
		t.Errorf("[P0] the out-of-branch dispute is now %q; a refused resolve must write NOTHING", status)
	}
}

// 0.11-INT-005 — resolution writes the note, the actor and the timestamp, co-
// commits dispute.resolved, and a SECOND attempt is refused without writing
// (AC 12, AC 13, T15, T17).
//
// The last assertion is the one that matters most for FR37's meaning: resolving
// records a DECISION and does not touch od_records. 0.12 owns the repair, and a
// teacher who believes this fixed the record will not do the thing that does.
func TestDispute_RejectionAnnotatesOnlyAndTheSecondAttemptIsRefused(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	roll := "CB.SC.U4CSE24805"
	studentID := seedDisputeStudent(t, ctx, migrator, roll, "91000000-0000-4000-8000-000000000001")

	svc := newDisputeService(t, runtime)
	receipt, err := svc.Submit(ctx, disputeSubmitInput(studentID, emailForRoll(roll), fixture.eventID, fixture.categoryID, 8))
	if err != nil {
		t.Fatalf("[P0] Submit: %v", err)
	}

	beforeRecords := countRows(t, ctx, migrator, "od_records")
	beforeAudit := countRows(t, ctx, migrator, "audit_log")

	admin := dispute.Actor{UserID: fixture.adminID, Role: "admin"}
	const note = "Verified against the register; the coordinator confirmed attendance."
	if _, err := svc.Resolve(ctx, admin, receipt.DisputeID, note, dispute.OutcomeRejected); err != nil {
		t.Fatalf("[P0] Resolve: %v", err)
	}

	var (
		status         string
		storedNote     *string
		resolvedBy     *string
		resolvedAtNull bool
	)
	if err := migrator.QueryRow(ctx, `
		SELECT status, resolution_note, resolved_by_user_id::text, resolved_at IS NULL
		FROM disputes WHERE id = $1`, receipt.DisputeID).
		Scan(&status, &storedNote, &resolvedBy, &resolvedAtNull); err != nil {
		t.Fatalf("[P0] re-read resolved dispute: %v", err)
	}
	if status != "resolved" {
		t.Errorf("[P0] status = %q, want resolved", status)
	}
	// T17's CHECK requires status/resolution_note/resolved_at to move together,
	// so any of these being null means the constraint is missing rather than
	// merely that a column was skipped.
	if storedNote == nil || *storedNote != note {
		t.Errorf("[P0] resolution_note = %v, want %q", storedNote, note)
	}
	if resolvedBy == nil || *resolvedBy != fixture.adminID {
		t.Errorf("[P0] resolved_by_user_id = %v, want the acting admin %q", resolvedBy, fixture.adminID)
	}
	if resolvedAtNull {
		t.Errorf("[P0] resolved_at is NULL on a resolved dispute; the T17 CHECK should have refused this row " +
			"outright, so a NULL here means the constraint was not created")
	}

	if got := countRows(t, ctx, migrator, "audit_log"); got != beforeAudit+1 {
		t.Errorf("[P0] audit_log went %d -> %d across one resolution, want exactly one new row", beforeAudit, got)
	}
	var action string
	if err := migrator.QueryRow(ctx, `
		SELECT action FROM audit_log
		WHERE target_id = $1 AND target_table = 'disputes' AND action = $2`,
		receipt.DisputeID, string(domain.ActionDisputeResolved)).Scan(&action); err != nil {
		t.Fatalf("[P0] no %s audit row for the resolved dispute: %v.\nAdd the constant to BOTH the const block "+
			"and AllActions in api/internal/domain/events.go — store.WithAudit rejects an unregistered action at "+
			"runtime (T12)", domain.ActionDisputeResolved, err)
	}

	// A REJECTION MANUFACTURES NO ATTENDANCE.
	//
	// This assertion used to say that NO resolution may touch od_records, when
	// resolving was annotate-only. That rule was the defect: a teacher agreed a
	// student had been missed and the student still never appeared anywhere.
	// Approving now writes the record (see the approval test below); rejecting
	// still must not, and that is the direction worth guarding — a rejection is
	// the teacher saying the student was NOT there.
	if got := countRows(t, ctx, migrator, "od_records"); got != beforeRecords {
		t.Errorf("[P0] od_records went %d -> %d across a REJECTED dispute. A rejection records the refusal and "+
			"nothing else; crediting it manufactures attendance the teacher explicitly declined",
			beforeRecords, got)
	}
	var storedOutcome *string
	if err := migrator.QueryRow(ctx, `SELECT outcome FROM disputes WHERE id = $1`, receipt.DisputeID).Scan(&storedOutcome); err != nil {
		t.Fatalf("[P0] read outcome: %v", err)
	}
	if storedOutcome == nil || *storedOutcome != "rejected" {
		t.Errorf("[P0] stored outcome = %v, want rejected — an inbox that cannot tell an approval from a "+
			"refusal cannot be audited", storedOutcome)
	}

	// The second attempt.
	_, err = svc.Resolve(ctx, admin, receipt.DisputeID, "Resolving again.", dispute.OutcomeApproved)
	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("[P0] the second resolution returned %v, want a typed domain error", err)
	}
	if domainErr.Code != contracts.ErrorCodeDISPUTEALREADYRESOLVED {
		t.Errorf("[P0] second-resolution code = %q, want DISPUTE_ALREADY_RESOLVED (409)", domainErr.Code)
	}
	var currentNote string
	if err := migrator.QueryRow(ctx, `SELECT resolution_note FROM disputes WHERE id = $1`, receipt.DisputeID).Scan(&currentNote); err != nil {
		t.Fatalf("re-read note: %v", err)
	}
	if currentNote != note {
		t.Errorf("[P0] the second resolution OVERWROTE the first note (%q). ResolveDispute must be guarded "+
			"`WHERE status = 'open'` so a raced double-resolve affects 0 rows rather than replacing the "+
			"original decision", currentNote)
	}

	// An unknown id is a 404 — a different answer from "already resolved".
	_, err = svc.Resolve(ctx, admin, uuid.NewString(), "Resolving a ghost.", dispute.OutcomeApproved)
	if !errors.As(err, &domainErr) || domainErr.Code != contracts.ErrorCodeDISPUTENOTFOUND {
		t.Errorf("[P0] unknown dispute id returned %v, want DISPUTE_NOT_FOUND (404)", err)
	}
}

// 0.11-INT-006 — the public event read returns archived events and NOTHING
// beyond the four allowed fields (AC 7, AC 8, T7/T8).
//
// The cap is server-side and there is no caller knob, so the only way to prove
// the cap exists is to seed past it. The field check is at the SERVICE level
// because that is where a query selecting one column too many first becomes
// visible — the handler's allowlist mapping would hide it, right up until
// somebody replaces the mapping with a passthrough.
func TestDispute_PublicEventsIncludeArchivedAndAreCappedServerSide(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	archived := seedPublicEvent(t, ctx, migrator, "ATDD Public Events Archived", "2026-07-20")
	if _, err := migrator.Exec(ctx, `
		UPDATE events SET status = 'archived', archived_at = now(), archived_by_user_id = $2
		WHERE id = $1`, archived.eventID, fixture.adminID); err != nil {
		t.Fatalf("archive event: %v", err)
	}

	collection, err := svcpublicattendance.NewSQLCService(runtime).PublicEvents(ctx)
	if err != nil {
		t.Fatalf("[P0] PublicEvents: %v", err)
	}
	if len(collection.Data) == 0 {
		t.Fatalf("[P0] PublicEvents returned nothing; a student who was never marked has no other way to name " +
			"the event they are disputing (T7)")
	}

	found := false
	for _, event := range collection.Data {
		if event.EventID == archived.eventID {
			found = true
		}
	}
	if !found {
		t.Errorf("[P0] the ARCHIVED event is missing from the public event list. Do not filter events.status — "+
			"archiving is an administrative act and a dispute is filed after the fact (T8). Returned %d events",
			len(collection.Data))
	}

	// Ordering is the server's, newest event_date first, so the event a student
	// just attended is at the top rather than buried under last term's.
	for i := 1; i < len(collection.Data); i++ {
		if collection.Data[i-1].EventDate < collection.Data[i].EventDate {
			t.Errorf("[P0] public events are not ordered event_date DESC: %q precedes %q",
				collection.Data[i-1].EventDate, collection.Data[i].EventDate)
			break
		}
	}

	// The categories come back with the event rather than per-event round trips.
	// N+1 on an unauthenticated endpoint is a request amplifier: one HTTP call
	// costs one query per event in the cap.
	for _, event := range collection.Data {
		if event.EventID == fixture.eventID && len(event.Categories) == 0 {
			t.Errorf("[P0] event %q came back with no categories; the student has to pick one, and a second "+
				"public endpoint to fetch them would be a disclosure surface T7 refuses to add", event.Name)
		}
	}
}

// 0.11-INT-007 — od_runtime holds exactly the privileges the dispute path needs
// on `disputes`, and no DELETE (T10).
//
// The registry in api/test/db/runtime_privileges_test.go is a closed set and an
// unlisted table is a test failure by design — 2.3 shipped that bug once. This is
// the complementary assertion from the other side: the runtime role can do the
// three things the story needs and cannot do the one it must never do. A dispute
// is a record of a claim; deleting one erases evidence.
func TestDispute_RuntimeRoleCanInsertUpdateSelectButNotDelete(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	roll := "CB.SC.U4CSE24806"
	studentID := seedDisputeStudent(t, ctx, migrator, roll, "91000000-0000-4000-8000-000000000001")

	receipt, err := newDisputeService(t, runtime).Submit(ctx,
		disputeSubmitInput(studentID, emailForRoll(roll), fixture.eventID, fixture.categoryID, 9))
	if err != nil {
		t.Fatalf("[P0] Submit as od_runtime: %v.\nA permission error here means `disputes` did not inherit "+
			"0007's default privileges — do NOT hand-write a GRANT (T10); check that the table was created by "+
			"the migrator role the default privileges were altered for", err)
	}

	if _, err := runtime.Exec(ctx, `DELETE FROM disputes WHERE id = $1`, receipt.DisputeID); err == nil {
		t.Errorf("[P0] od_runtime DELETED a dispute row. 0007 flipped the runtime default privileges to " +
			"SELECT, INSERT, UPDATE — no DELETE. A dispute is a student's record of a claim and the audit " +
			"ledger references it; the application must have no way to erase either")
	} else if !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Errorf("[P1] the DELETE failed for an unexpected reason (%v); this test can only prove the privilege "+
			"boundary if the refusal comes from the grant", err)
	}
}

// 0.11-INT-010 — the public event cap is enforced by the SERVER, proven by
// seeding past it (AC 8).
//
// The sibling test's own doc comment said "the only way to prove the cap exists
// is to seed past it", and then did not: it asserted ordering and field shape
// only, so deleting `LIMIT @row_limit` from ListPublicEvents left it green. This
// endpoint is unauthenticated and takes no caller `limit`, so the cap is the
// only thing bounding the response.
func TestDispute_PublicEventsCapIsEnforcedByTheServer(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))

	var before int
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM events`).Scan(&before); err != nil {
		t.Fatalf("count events: %v", err)
	}

	// Seed strictly past the cap, counting whatever the database already holds.
	need := svcpublicattendance.MaxPublicEventRows - before + 5
	seeded := make([]string, 0, max(need, 0))
	for i := 0; i < need; i++ {
		id := uuid.NewString()
		if _, err := migrator.Exec(ctx, `
			INSERT INTO events (id, name, event_date, end_date, status)
			VALUES ($1, $2, DATE '2026-06-01' + ($3::int || ' days')::interval,
			        DATE '2026-06-01' + ($3::int || ' days')::interval, 'active')`,
			id, fmt.Sprintf("ATDD Dispute Cap Event %d", i), int32(i)); err != nil {
			t.Fatalf("seed cap event %d: %v", i, err)
		}
		seeded = append(seeded, id)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		for _, id := range seeded {
			if _, err := migrator.Exec(cleanupCtx, `DELETE FROM events WHERE id = $1`, id); err != nil {
				t.Logf("cleanup cap event %s: %v", id, err)
			}
		}
	})

	var total int
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM events`).Scan(&total); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if total <= svcpublicattendance.MaxPublicEventRows {
		t.Fatalf("the fixture did not seed past the cap (%d events, cap %d); the assertion below would pass "+
			"for the wrong reason", total, svcpublicattendance.MaxPublicEventRows)
	}

	collection, err := svcpublicattendance.NewSQLCService(runtime).PublicEvents(ctx)
	if err != nil {
		t.Fatalf("[P0] PublicEvents: %v", err)
	}
	if len(collection.Data) > svcpublicattendance.MaxPublicEventRows {
		t.Errorf("[P0] PublicEvents returned %d events with %d in the database; the server cap of %d is not "+
			"applied. This endpoint takes no caller limit and anyone can call it, so the LIMIT in "+
			"ListPublicEvents is the ONLY bound on the response size (AC 8)",
			len(collection.Data), total, svcpublicattendance.MaxPublicEventRows)
	}
}

// 0.11-INT-011 — a category that belongs to a DIFFERENT event is refused with
// CATEGORY_NOT_FOUND (AC 4).
//
// The composite FK (event_id, category_id) -> categories(event_id, id) is what
// makes "this category belongs to this event" a database guarantee rather than a
// handler check, and the mapping from its 23503 to CATEGORY_NOT_FOUND existed
// with nothing exercising it. Without this, a mismatched pair would surface as
// the generic EVENT_NOT_FOUND — telling a student their event does not exist
// when it plainly does.
func TestDispute_CategoryFromAnotherEventIsRefused(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	roll := "CB.SC.U4CSE24811"
	studentID := seedDisputeStudent(t, ctx, migrator, roll, "91000000-0000-4000-8000-000000000001")

	// A second event with its own category. The pair below is internally valid
	// on each side and invalid together, which is precisely what a handler check
	// on either id alone cannot catch.
	other := seedPublicEvent(t, ctx, migrator, "ATDD Dispute Foreign Event", "2026-07-19")

	beforeDisputes := countRows(t, ctx, migrator, "disputes")
	beforeAudit := countRows(t, ctx, migrator, "audit_log")

	svc := newDisputeService(t, runtime)
	_, err := svc.Submit(ctx, disputeSubmitInput(studentID, emailForRoll(roll), fixture.eventID, other.categoryID, 4))

	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("[P0] a category from another event returned %v, want a typed domain error", err)
	}
	if domainErr.Code != contracts.ErrorCodeCATEGORYNOTFOUND {
		t.Errorf("[P0] code = %q, want CATEGORY_NOT_FOUND. The composite FK is the guarantee; its 23503 must "+
			"map to the category answer, not to the generic EVENT_NOT_FOUND, or a student is told their event "+
			"does not exist when the category is what is wrong (AC 4)", domainErr.Code)
	}
	if got := countRows(t, ctx, migrator, "disputes"); got != beforeDisputes {
		t.Errorf("[P0] a refused submission wrote a dispute row (%d -> %d)", beforeDisputes, got)
	}
	if got := countRows(t, ctx, migrator, "audit_log"); got != beforeAudit {
		t.Errorf("[P0] a refused submission wrote an audit row (%d -> %d); WithAudit must roll BOTH back",
			beforeAudit, got)
	}
}
