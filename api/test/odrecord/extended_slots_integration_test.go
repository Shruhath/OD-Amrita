//go:build integration

// Real-Postgres tests for the EXTENDED SLOTS switch.
//
// The switch decides what the marking path accepts, so the thing worth proving
// is not that a boolean round-trips — it is that the DATABASE agrees. Slot 14
// is legal only because migration 0019 widened a CHECK constraint; a service
// that accepted it while the constraint still said 12 would turn a validation
// error into an opaque 500 at write time, which is precisely the failure mode
// a stub cannot see.
//
// In the EXISTING api/test/odrecord package: ./test/odrecord/... is already
// wired into api/Makefile's INTEGRATION_PKGS and api-ci.yml.
package odrecordtest

import (
	"testing"

	"github.com/google/uuid"

	"github.com/Shruhath/OD-Amrita/api/internal/service/appsettings"
	odrecord "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
)

// SLOTS-INT-001 — the switch defaults OFF, and OFF is the behaviour production
// already had.
//
// The default is the whole safety property of this change: migration 0019 runs
// on the eve of an event, and it must not quietly open eight new slots.
func TestExtendedSlots_DefaultsOff(t *testing.T) {
	ctx := testCtx(t)
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))

	enabled, err := appsettings.NewService(runtime).ExtendedSlotsEnabled(ctx)
	if err != nil {
		t.Fatalf("[P0] read the switch: %v", err)
	}
	if enabled {
		t.Fatal("[P0] extended slots are ON in a freshly migrated database. The seeded default must be OFF — " +
			"a migration applied before an event must not change what the app accepts")
	}
	if got := odrecord.SlotCount(enabled); got != 12 {
		t.Errorf("[P0] the ceiling with the switch off is %d, want the teaching day's 12", got)
	}
}

// SLOTS-INT-002 — flipping it takes effect, and flipping it back restores the
// old ceiling.
//
// Both directions, because a switch that cannot be turned off is not a switch:
// this exists so an evening rehearsal can be undone before the real event.
func TestExtendedSlots_TogglesBothWays(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	fixture := seedMarkFixture(t, ctx, migrator)

	service := appsettings.NewService(runtime)
	t.Cleanup(func() {
		// Leave the shared database as it was found. Another test in this
		// package asserts the default, and it runs in the same process.
		_, _, _ = service.SetExtendedSlots(ctx, false, fixture.adminID)
	})

	enabled, _, err := service.SetExtendedSlots(ctx, true, fixture.adminID)
	if err != nil {
		t.Fatalf("[P0] turn the switch on: %v", err)
	}
	if !enabled {
		t.Fatal("[P0] the write reported the switch still off")
	}
	// Read back through the SERVICE, which caches. A stale read here would mean
	// an administrator flips the toggle and the marking path keeps refusing.
	nowOn, err := service.ExtendedSlotsEnabled(ctx)
	if err != nil {
		t.Fatalf("[P0] read back: %v", err)
	}
	if !nowOn {
		t.Fatal("[P0] the switch read back as OFF immediately after being turned on — the cache was not " +
			"invalidated by the write, so the toggle looks broken to the person who just used it")
	}
	if got := odrecord.SlotCount(nowOn); got != 20 {
		t.Errorf("[P0] the ceiling with the switch on is %d, want 20", got)
	}

	if _, _, err = service.SetExtendedSlots(ctx, false, fixture.adminID); err != nil {
		t.Fatalf("[P0] turn the switch off: %v", err)
	}
	nowOff, err := service.ExtendedSlotsEnabled(ctx)
	if err != nil {
		t.Fatalf("[P0] read back after turning off: %v", err)
	}
	if nowOff {
		t.Fatal("[P0] the switch would not turn back off. The evening slots must be undoable before a real event")
	}
}

// SLOTS-INT-003 — THE DATABASE ACCEPTS SLOT 20, AND REFUSES 21.
//
// The one assertion a stub could never make. The service's ceiling is only
// meaningful if the CHECK constraint behind it agrees; before migration 0019 a
// slot of 14 was refused by Postgres no matter what the application thought.
//
// 21 is asserted from the other side: the widened constraint must still be a
// constraint, or the ceiling is decoration.
func TestExtendedSlots_DatabaseAcceptsTwentyAndRefusesTwentyOne(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	fixture := seedMarkFixture(t, ctx, migrator)
	day := "2026-09-21"
	event := seedBoardEvent(t, ctx, migrator, day)

	// Slot 20 — legal only because 0019 widened the CHECK.
	seedPublicRecord(t, ctx, migrator, fixture.studentID, event.eventID, event.categoryID, 20,
		fixture.coordinatorID, "coordinator", day)

	var stored int
	if err := migrator.QueryRow(ctx,
		`SELECT slot FROM od_records WHERE student_id = $1 AND od_date = $2::date AND slot = 20`,
		fixture.studentID, day).Scan(&stored); err != nil {
		t.Fatalf("[P0] slot 20 did not survive the write: %v. Migration 0019 is what widens the CHECK — "+
			"without it the service would accept 20 and Postgres would answer with a 500", err)
	}

	// And 21 is still refused. A constraint that admits everything is not one.
	_, err := migrator.Exec(ctx, `
		INSERT INTO od_records
			(id, student_id, event_id, category_id, slot, od_date, status, capture_method, actor_user_id, actor_role, reason_note, server_received_at)
		VALUES ($1, $2, $3, $4, 21, $5::date, 'present', 'search', $6, 'coordinator', 'ceiling probe', now())`,
		uuid.NewString(), fixture.studentID, event.eventID, event.categoryID, day, fixture.coordinatorID)
	if err == nil {
		t.Fatal("[P0] the database accepted slot 21. The CHECK was widened to 20, not removed — a missing " +
			"upper bound means any integer becomes a slot")
	}
}

// SLOTS-INT-004 — the evening windows are real, contiguous, and end before
// midnight.
//
// The midnight bound is the one that matters. `od_date` is a campus day, so a
// window that opened on one day and closed on the next would file a mark under
// a day the slot did not belong to — and the marking path derives the day from
// the clock, not from the slot.
func TestExtendedSlots_EveningWindowsStayInsideOneCampusDay(t *testing.T) {
	for slot := 13; slot <= 20; slot++ {
		window, ok := odrecord.SlotWindowFor(slot)
		if !ok {
			t.Fatalf("[P0] slot %d has no window, so the switch would open a slot nothing can be marked in", slot)
		}
		if window.EndHour > 23 || (window.EndHour == 23 && window.EndMinute > 59) {
			t.Errorf("[P0] slot %d ends at %02d:%02d, which crosses into the next campus day",
				slot, window.EndHour, window.EndMinute)
		}
		if slot > 13 {
			previous, _ := odrecord.SlotWindowFor(slot - 1)
			if previous.EndHour != window.StartHour || previous.EndMinute != window.StartMinute {
				t.Errorf("[P0] slot %d starts at %02d:%02d but slot %d ended at %02d:%02d — a gap between "+
					"evening slots is a stretch of the evening nobody can be marked in",
					slot, window.StartHour, window.StartMinute, slot-1, previous.EndHour, previous.EndMinute)
			}
		}
	}
}
