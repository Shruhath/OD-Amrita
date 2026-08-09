//go:build integration

package db

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A teacher/admin manual write needs an explicit reason, while the existing
// coordinator capture path remains valid without one. The database is the
// backstop: a direct runtime-role write must not be able to bypass the service.
func TestRecordManagementSchema_ManualReasonIsRequiredWithoutChangingCoordinatorMarks(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	fixture := seedRangeFixture(t, ctx, migrator, "2026-08-10", "2026-08-10")

	dataType, nullable := columnShape(t, ctx, migrator, "od_records", "reason_note")
	if dataType != "text" || nullable != "YES" {
		t.Errorf("[P0] od_records.reason_note shape = %s nullable=%s, want nullable text", dataType, nullable)
	}
	if !constraintExists(t, ctx, migrator, "od_records", "od_records_manual_reason_note_ck") {
		t.Fatal("[P0] od_records_manual_reason_note_ck is missing")
	}
	var validated bool
	if err := migrator.QueryRow(ctx, `
		SELECT convalidated FROM pg_constraint
		WHERE conrelid = 'od_records'::regclass AND conname = 'od_records_manual_reason_note_ck'`).Scan(&validated); err != nil {
		t.Fatalf("read manual-reason constraint validation state: %v", err)
	}
	if validated {
		t.Fatal("[P0] od_records_manual_reason_note_ck is VALIDATED; historical manual records must not be backfilled or rejected")
	}

	_, err := runtime.Exec(ctx, `
		INSERT INTO od_records
			(id, student_id, event_id, category_id, slot, od_date, status, capture_method,
			 actor_user_id, actor_role)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 1, '2026-08-10', 'present', 'search',
		        $5::uuid, 'teacher')`,
		uuid.NewString(), fixture.studentID, fixture.eventID, fixture.categoryID, fixture.actorID)
	if !violatesConstraint(err, "23514", "od_records_manual_reason_note_ck") {
		t.Fatalf("[P0] a new teacher record without a reason was accepted or refused by the wrong constraint: %v", err)
	}

	_, err = runtime.Exec(ctx, `
		INSERT INTO od_records
			(id, student_id, event_id, category_id, slot, od_date, status, capture_method,
			 actor_user_id, actor_role, reason_note)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 2, '2026-08-10', 'present', 'search',
		        $5::uuid, 'teacher', repeat('x', 2001))`,
		uuid.NewString(), fixture.studentID, fixture.eventID, fixture.categoryID, fixture.actorID)
	if !violatesConstraint(err, "23514", "od_records_reason_note_length_ck") {
		t.Fatalf("[P0] a reason longer than 2000 characters was accepted or refused by the wrong constraint: %v", err)
	}

	if err := insertODRecord(ctx, runtime, fixture, "2026-08-10", 2); err != nil {
		t.Fatalf("[P0] a coordinator capture without a reason was refused: %v", err)
	}
}

// NOT VALID is not a cosmetic modifier: it preserves old teacher/admin marks
// whose real historical reason was never captured, while still protecting all
// writes made after the migration.
func TestRecordManagementSchema_NotValidReasonCheckPreservesLegacyManualRows(t *testing.T) {
	requireGoose(t)
	ctx := testCtx(t)
	dsn := dsnOrSkip(t, envMigratorDSN)
	pool := connect(t, ctx, dsn)
	t.Cleanup(func() { runGoose(t, dsn, "up") })

	clearRollbackBlockers(t, ctx, pool)
	runGoose(t, dsn, "reset")
	runGoose(t, dsn, "up-to", "17")
	fixture := seedRangeFixture(t, ctx, pool, "2026-08-10", "2026-08-10")
	legacyID := uuid.NewString()
	mustExec(t, ctx, pool, "seed pre-0018 teacher record", `
		INSERT INTO od_records
			(id, student_id, event_id, category_id, slot, od_date, status, capture_method,
			 actor_user_id, actor_role)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 1, '2026-08-10', 'present', 'search',
		        $5::uuid, 'teacher')`,
		legacyID, fixture.studentID, fixture.eventID, fixture.categoryID, fixture.actorID)

	runGoose(t, dsn, "up")
	var reason *string
	if err := pool.QueryRow(ctx, `SELECT reason_note FROM od_records WHERE id = $1::uuid`, legacyID).Scan(&reason); err != nil {
		t.Fatalf("read legacy teacher record after 0018: %v", err)
	}
	if reason != nil {
		t.Fatalf("[P0] 0018 invented a reason for a legacy teacher record: %q", *reason)
	}
}

// 0018's Down must refuse a destructive rollback after a removal rather than
// rewriting the fact as present or deleting it. Goose executes the step in a
// transaction, so failure leaves version 18 and the removed row intact.
func TestRecordManagementSchema_DownRefusesAfterRemovedRecord(t *testing.T) {
	requireGoose(t)
	ctx := testCtx(t)
	dsn := dsnOrSkip(t, envMigratorDSN)
	pool := connect(t, ctx, dsn)

	fixture := seedRangeFixture(t, ctx, pool, "2026-08-10", "2026-08-10")
	removedID := uuid.NewString()
	mustExec(t, ctx, pool, "seed removed record", `
		INSERT INTO od_records
			(id, student_id, event_id, category_id, slot, od_date, status, capture_method,
			 actor_user_id, actor_role, reason_note)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 1, '2026-08-10', 'removed', 'search',
		        $5::uuid, 'teacher', 'duplicate entry removed')`,
		removedID, fixture.studentID, fixture.eventID, fixture.categoryID, fixture.actorID)

	// `down-to 17`, NOT a bare `down`.
	//
	// A bare `down` reverts whatever happens to be newest, so this test silently
	// stopped exercising 0018 the moment 0019 was added — it reverted 0019
	// instead, which succeeds, and reported 0018 as broken while leaving the
	// database a version behind for everything that ran afterwards. Naming the
	// target keeps the test about the migration it claims to be about however
	// many land on top of it.
	//
	// It also has to come back UP afterwards: this suite shares one database and
	// runs serially, so a test that leaves the schema rolled back breaks every
	// test after it rather than its own assertion.
	argv := []string{"-dir", migrationsDir(t), "postgres", dsn, "down-to", "17"}
	output, err := exec.Command("goose", argv...).CombinedOutput()
	t.Cleanup(func() {
		up := exec.Command("goose", "-dir", migrationsDir(t), "postgres", dsn, "up")
		if restored, upErr := up.CombinedOutput(); upErr != nil {
			t.Fatalf("could not restore the schema after the rollback probe: %v\n%s", upErr, restored)
		}
	})
	if err == nil {
		t.Fatal("[P0] 0018 Down succeeded after a removed record; it must never erase or rewrite attendance history")
	}
	if !strings.Contains(string(output), "od_records_status_check") {
		t.Fatalf("[P0] 0018 Down failed for an unexpected reason, want the restored status check: %s", output)
	}

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM od_records WHERE id = $1::uuid`, removedID).Scan(&status); err != nil {
		t.Fatalf("read removed record after failed Down: %v", err)
	}
	if status != "removed" {
		t.Fatalf("[P0] failed Down changed record status to %q, want removed", status)
	}
}
