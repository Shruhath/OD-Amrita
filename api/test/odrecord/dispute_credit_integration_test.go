//go:build integration

// Real-Postgres tests for what an APPROVED dispute actually does.
//
// THE DEFECT THIS FILE EXISTS FOR. Resolving a dispute used to be an annotation
// and nothing else. A student reported a missed slot, a teacher agreed, wrote a
// note — and the student still did not appear in od_records or on the public
// dashboard. Crediting the slot was a separate trip to the record-management
// surface that nothing prompted and nothing tracked, so in practice the slot the
// student disputed was simply never credited.
//
// These run against Postgres deliberately. The service tests observe what the
// repository was ASKED to do, which is the right seam for the approve/reject
// decision but cannot tell you whether the row survives the FR10 duplicate key,
// the event-range trigger, the reason-note CHECK and the widened capture_method
// CHECK — and it is the row landing that the student's day depends on.
package odrecordtest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/service/dispute"
	"github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
)

// creditedRecord is the od_records row an approval produced, read back from the
// database rather than from the service's own answer.
type creditedRecord struct {
	slot          int
	odDate        string
	captureMethod string
	actorRole     string
	actorUserID   *string
	reasonNote    *string
	status        string
}

func readCreditedRecord(t *testing.T, ctx context.Context, pool *pgxpool.Pool, studentID, eventID string) creditedRecord {
	t.Helper()
	var got creditedRecord
	if err := pool.QueryRow(ctx, `
		SELECT slot, od_date::text, capture_method, actor_role, actor_user_id::text, reason_note, status
		FROM od_records
		WHERE student_id = $1 AND event_id = $2`, studentID, eventID).
		Scan(&got.slot, &got.odDate, &got.captureMethod, &got.actorRole, &got.actorUserID, &got.reasonNote, &got.status); err != nil {
		t.Fatalf("[P0] no od_records row for the approved dispute: %v", err)
	}
	return got
}

// DISPUTE-CREDIT-001 — THE REPORTED DEFECT, end to end.
//
// Approving writes the record, with the disputed student, day and slot — and the
// student then appears on the public dashboard for that day, which is where they
// go to check.
func TestDispute_ApprovalCreditsTheSlotAndReachesThePublicDashboard(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	roll := "CB.SC.U4CSE24821"
	studentID := seedDisputeStudent(t, ctx, migrator, roll, "91000000-0000-4000-8000-000000000001")

	svc := newDisputeService(t, runtime)
	const slot = 5
	receipt, err := svc.Submit(ctx, disputeSubmitInput(studentID, emailForRoll(roll), fixture.eventID, fixture.categoryID, slot))
	if err != nil {
		t.Fatalf("[P0] Submit: %v", err)
	}

	admin := dispute.Actor{UserID: fixture.adminID, Role: "admin"}
	const note = "Coordinator confirmed the student was present; the tap did not register."
	resolved, err := svc.Resolve(ctx, admin, receipt.DisputeID, note, dispute.OutcomeApproved)
	if err != nil {
		t.Fatalf("[P0] Resolve(approved): %v", err)
	}

	// 1. The record exists, and carries the DISPUTED facts.
	got := readCreditedRecord(t, ctx, migrator, studentID, fixture.eventID)
	if got.slot != slot {
		t.Errorf("[P0] credited slot %d, want the disputed %d", got.slot, slot)
	}
	if got.odDate != fixtureODDate {
		t.Errorf("[P0] credited od_date %q, want the disputed day %q — filing it under today would put the "+
			"student on the wrong day of the festival", got.odDate, fixtureODDate)
	}
	if got.status != "present" {
		t.Errorf("[P0] status = %q, want present", got.status)
	}
	// 2. It says how it came to exist. Recording this as a `search` would be a
	//    lie in the one table whose job is to say who marked a student and how.
	if got.captureMethod != "dispute" {
		t.Errorf("[P0] capture_method = %q, want dispute", got.captureMethod)
	}
	if got.actorRole != "admin" || got.actorUserID == nil || *got.actorUserID != fixture.adminID {
		t.Errorf("[P0] the record does not name the resolver: role=%q user=%v", got.actorRole, got.actorUserID)
	}
	if got.reasonNote == nil || *got.reasonNote != note {
		t.Errorf("[P0] reason_note = %v, want the resolution note", got.reasonNote)
	}
	// 3. The response tells the teacher the slot was credited, not merely that
	//    the dispute was closed.
	if resolved.ODRecordID == nil {
		t.Error("[P0] the resolution reported no od_record_id, so the teacher's screen cannot distinguish " +
			"a credited approval from a closed dispute")
	}
	if resolved.Outcome == nil || *resolved.Outcome != dispute.OutcomeApproved {
		t.Errorf("[P0] the resolved dispute reports outcome %v, want approved", resolved.Outcome)
	}

	// 4. AND IT IS ON THE PUBLIC DASHBOARD. This is the half the student sees,
	//    and the half that was missing.
	board, err := publicattendance.NewSQLCService(runtime).Day(ctx, publicattendance.DayFilter{ODDate: fixtureODDate})
	if err != nil {
		t.Fatalf("[P0] Day: %v", err)
	}
	marks := boardMarksFor(t, board, roll)
	if len(marks) != 1 {
		t.Fatalf("[P0] the public board shows %d marks for %s, want the 1 the approval credited — the student "+
			"checks this page, and an approval they cannot see is indistinguishable from one that never happened",
			len(marks), roll)
	}
	if marks[0].Slot != slot {
		t.Errorf("[P0] the board shows slot %d, want %d", marks[0].Slot, slot)
	}
}

// DISPUTE-CREDIT-002 — an already-marked slot is MOOT, not failed.
//
// A coordinator catching up, or a teacher fixing the record by hand while the
// dispute sat open, must not turn the approval into an error: the end state the
// teacher asked for already holds. The dispute resolves, and no duplicate row is
// written — the FR10 per-day key would refuse one anyway, and a 500 in a
// teacher's face is the wrong way to learn that.
func TestDispute_ApprovingAnAlreadyMarkedSlotResolvesWithoutDuplicating(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	roll := "CB.SC.U4CSE24822"
	studentID := seedDisputeStudent(t, ctx, migrator, roll, "91000000-0000-4000-8000-000000000001")

	svc := newDisputeService(t, runtime)
	const slot = 4
	receipt, err := svc.Submit(ctx, disputeSubmitInput(studentID, emailForRoll(roll), fixture.eventID, fixture.categoryID, slot))
	if err != nil {
		t.Fatalf("[P0] Submit: %v", err)
	}

	// Somebody gets there first.
	seedPublicRecord(t, ctx, migrator, studentID, fixture.eventID, fixture.categoryID, slot, fixture.coordinatorID, "coordinator", fixtureODDate)
	before := countRows(t, ctx, migrator, "od_records")

	admin := dispute.Actor{UserID: fixture.adminID, Role: "admin"}
	resolved, err := svc.Resolve(ctx, admin, receipt.DisputeID, "Already marked; closing.", dispute.OutcomeApproved)
	if err != nil {
		t.Fatalf("[P0] approving an already-marked slot failed with %v; the desired end state already held", err)
	}
	if got := countRows(t, ctx, migrator, "od_records"); got != before {
		t.Errorf("[P0] od_records went %d -> %d; the approval duplicated a mark that already existed", before, got)
	}
	if resolved.ODRecordID != nil {
		t.Errorf("[P0] reported od_record_id %v for a record it did not create", *resolved.ODRecordID)
	}
	var status string
	if err := migrator.QueryRow(ctx, `SELECT status FROM disputes WHERE id = $1`, receipt.DisputeID).Scan(&status); err != nil {
		t.Fatalf("read dispute: %v", err)
	}
	if status != "resolved" {
		t.Errorf("[P0] the dispute is %q, want resolved — a moot approval still closes the dispute", status)
	}
}

// DISPUTE-CREDIT-003 — BOTH FACTS COMMIT, OR NEITHER DOES.
//
// The teacher must never be shown "resolved" over a slot that was not credited.
// An archived event is the reachable way to force the record write to fail:
// disputes stay submittable and resolvable against archived events by design
// (0014), while od_records_active_event_guard refuses the record — so an
// approval here has to fail as a whole rather than resolve the dispute and
// quietly skip the credit.
func TestDispute_AnApprovalThatCannotWriteTheRecordResolvesNothing(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	roll := "CB.SC.U4CSE24823"
	studentID := seedDisputeStudent(t, ctx, migrator, roll, "91000000-0000-4000-8000-000000000001")

	svc := newDisputeService(t, runtime)
	receipt, err := svc.Submit(ctx, disputeSubmitInput(studentID, emailForRoll(roll), fixture.eventID, fixture.categoryID, 3))
	if err != nil {
		t.Fatalf("[P0] Submit: %v", err)
	}

	// events_archive_state_ck requires the archive columns to move together with
	// the status, so a bare status flip is refused by the schema.
	if _, err := migrator.Exec(ctx, `
		UPDATE events SET status = 'archived', archived_at = now(), archived_by_user_id = $2
		WHERE id = $1`, fixture.eventID, fixture.adminID); err != nil {
		t.Fatalf("archive event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = migrator.Exec(context.Background(), `
			UPDATE events SET status = 'active', archived_at = NULL, archived_by_user_id = NULL
			WHERE id = $1`, fixture.eventID)
	})

	before := countRows(t, ctx, migrator, "od_records")
	admin := dispute.Actor{UserID: fixture.adminID, Role: "admin"}
	if _, err := svc.Resolve(ctx, admin, receipt.DisputeID, "Approving after archive.", dispute.OutcomeApproved); err == nil {
		t.Fatal("[P0] the approval succeeded although the record could not be written")
	}

	var status string
	if err := migrator.QueryRow(ctx, `SELECT status FROM disputes WHERE id = $1`, receipt.DisputeID).Scan(&status); err != nil {
		t.Fatalf("read dispute: %v", err)
	}
	if status != "open" {
		t.Errorf("[P0] the dispute is %q after a failed credit, want open. Reporting it resolved over a slot "+
			"that was never credited is exactly the defect this change removes", status)
	}
	if got := countRows(t, ctx, migrator, "od_records"); got != before {
		t.Errorf("[P0] od_records went %d -> %d on a rolled-back approval", before, got)
	}
}
