// Red-phase unit tests for the three date primitives the multi-day event needs.
//
// COMPILE-RED on purpose. `ActiveSlot` gains an end bound and two new functions
// appear beside it, so this file does not build until the dev writes them. That
// makes the whole `odrecord` package red, which is loud — and correct: every
// caller of ActiveSlot has to be revisited, and a signature change that compiled
// would mean a caller was silently left on the old single-day behaviour.
//
// PRESCRIBED SURFACE (written here so the dev is not reverse-engineering it from
// compiler errors):
//
//	func ActiveSlot(now, eventStart, eventEnd time.Time, StandardSlotCount) (int, SlotWindow, bool)
//	func CampusDate(now time.Time) time.Time
//	func ClampToEventRange(day, eventStart, eventEnd time.Time) time.Time
//
// `CampusDate` is deliberately NOT a fourth return value from `ActiveSlot` (T13):
// the credit debit and the two counters need the day even when no slot is open,
// and folding it into ActiveSlot would make those paths ask a question about
// slots in order to learn the date.
//
// NOT gated behind a build tag. `od_record_atdd_test.go` next door carries
// `//go:build atdd` and NOTHING in api/Makefile or .github/workflows passes
// `-tags atdd`, so that entire file has never run. A skipped test and a missing
// test are the same signal.
package odrecord

import (
	"testing"
	"time"
)

func campusTime(t *testing.T, year int, month time.Month, day, hour, minute int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatalf("load campus location: %v", err)
	}
	return time.Date(year, month, day, hour, minute, 0, 0, loc)
}

func campusMidnight(t *testing.T, year int, month time.Month, day int) time.Time {
	t.Helper()
	return campusTime(t, year, month, day, 0, 0)
}

// 0.12a-UNIT-001 — the slot gate opens on RANGE CONTAINMENT, and it is INCLUSIVE
// at both ends.
//
// `schedule.go` compares the campus date of `now` against the campus date of a
// single `eventDate`. The whole feature is that comparison becoming containment.
// Both bounds are asserted because an exclusive comparison makes the first and
// last day of a festival unmarkable — which is exactly when marking happens.
func TestActiveSlot_OpensOnEveryDayInsideTheRangeInclusive(t *testing.T) {
	start := campusMidnight(t, 2026, time.August, 10)
	end := campusMidnight(t, 2026, time.September, 8)

	for _, tc := range []struct {
		name string
		now  time.Time
	}{
		{"the first day of the range", campusTime(t, 2026, time.August, 10, 8, 25)},
		{"a day in the middle", campusTime(t, 2026, time.August, 19, 8, 25)},
		{"the last day of the range", campusTime(t, 2026, time.September, 8, 8, 25)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slot, window, open := ActiveSlot(tc.now, start, end, StandardSlotCount)
			if !open {
				t.Fatalf("[P0] no slot open on %s (%s). Containment is INCLUSIVE at both ends — an exclusive "+
					"comparison makes day one and the final day of a festival unmarkable",
					tc.name, tc.now.Format("2006-01-02"))
			}
			if slot != 1 {
				t.Errorf("[P0] slot = %d, want 1 at 08:25 campus time", slot)
			}
			// The window must be anchored to TODAY, not to the event's start date.
			// A window built from `eventStart` would hand every coordinator on day
			// 19 a countdown that expired nine days earlier.
			if got := window.Start.In(tc.now.Location()).Format("2006-01-02"); got != tc.now.Format("2006-01-02") {
				t.Errorf("[P0] window.Start is dated %s but today is %s. The slot scan must run against TODAY's "+
					"date; anchoring it to the event's start date gives every coordinator after day one a "+
					"countdown that already expired", got, tc.now.Format("2006-01-02"))
			}
		})
	}
}

// 0.12a-UNIT-002 — outside the range there is NO slot, at either end.
//
// The negative control for UNIT-001. A range check that returned true for
// everything would satisfy the test above completely.
func TestActiveSlot_StaysClosedOutsideTheRange(t *testing.T) {
	start := campusMidnight(t, 2026, time.August, 10)
	end := campusMidnight(t, 2026, time.September, 8)

	for _, tc := range []struct {
		name string
		now  time.Time
	}{
		{"the day before the range starts", campusTime(t, 2026, time.August, 9, 8, 25)},
		{"the day after the range ends", campusTime(t, 2026, time.September, 9, 8, 25)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if slot, _, open := ActiveSlot(tc.now, start, end, StandardSlotCount); open {
				t.Errorf("[P0] slot %d reported open on %s, which is outside the event's range", slot, tc.name)
			}
		})
	}
}

// 0.12a-UNIT-003 — a single-day event behaves EXACTLY as it does today.
//
// AC 2 is the compatibility promise, and this is where it is cheapest to check.
// Every pre-existing event is backfilled to `end_date = event_date`, so if
// containment were exclusive at the upper bound, the migration would silently
// close the slot gate on every event already in the database.
func TestActiveSlot_SingleDayEventIsUnchanged(t *testing.T) {
	day := campusMidnight(t, 2026, time.August, 3)

	if _, _, open := ActiveSlot(campusTime(t, 2026, time.August, 3, 8, 25), day, day, StandardSlotCount); !open {
		t.Errorf("[P0] a single-day event (end_date == event_date) has no open slot during its own slot 1. " +
			"Every pre-existing event is backfilled to this exact shape, so the migration would close the " +
			"marking screen on the whole database")
	}
	if _, _, open := ActiveSlot(campusTime(t, 2026, time.August, 4, 8, 25), day, day, StandardSlotCount); open {
		t.Errorf("[P0] a single-day event accepted the following day")
	}
	// And the out-of-hours behaviour is untouched: inside the range but between
	// slots is still "no slot", which the screen renders as "nothing scheduled"
	// rather than as an error (AC 11).
	if _, _, open := ActiveSlot(campusTime(t, 2026, time.August, 3, 19, 0), day, day, StandardSlotCount); open {
		t.Errorf("[P0] a slot was reported open at 19:00, after the last slot ends at 18:15")
	}
}

// 0.12a-UNIT-004 — `CampusDate` returns campus-local midnight, and the +05:30
// offset is honoured rather than approximated.
//
// India has no DST, so there is no ambiguous-hour case — but the offset is
// THIRTY MINUTES off an hour boundary, and a hand-rolled `+5h` truncation is
// wrong by exactly that much at the day boundary. A mark taken at 00:15 IST would
// then be filed under the previous day, every night, for fifteen minutes.
func TestCampusDate_IsCampusLocalMidnightAcrossTheOffset(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  time.Time
		want string
	}{
		{
			name: "just after campus midnight",
			now:  campusTime(t, 2026, time.August, 19, 0, 15),
			want: "2026-08-19",
		},
		{
			name: "just before campus midnight",
			now:  campusTime(t, 2026, time.August, 19, 23, 45),
			want: "2026-08-19",
		},
		{
			// 19:00 UTC on the 18th is 00:30 IST on the 19th. A naive UTC date, or
			// a +5h truncation, both answer the 18th.
			name: "an instant that is a different date in UTC",
			now:  time.Date(2026, time.August, 18, 19, 0, 0, 0, time.UTC),
			want: "2026-08-19",
		},
		{
			// 18:45 UTC on the 18th is 00:15 IST on the 19th. This is the case a
			// +5h offset gets wrong and a +05:30 offset gets right.
			name: "inside the thirty minutes a +5h offset would misfile",
			now:  time.Date(2026, time.August, 18, 18, 45, 0, 0, time.UTC),
			want: "2026-08-19",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CampusDate(tc.now)
			if got.Format("2006-01-02") != tc.want {
				t.Errorf("[P0] CampusDate(%s) = %s, want %s. Do NOT hand-roll the offset as +5h — Asia/Kolkata "+
					"is +05:30 and a naive truncation is wrong by thirty minutes at the day boundary",
					tc.now.Format(time.RFC3339), got.Format("2006-01-02"), tc.want)
			}
			if h, m, s := got.Clock(); h != 0 || m != 0 || s != 0 {
				t.Errorf("[P0] CampusDate returned %02d:%02d:%02d, want campus-local midnight", h, m, s)
			}
		})
	}
}

// 0.12a-UNIT-005 — the teacher/admin clamp, in all three directions.
//
// T14. Coordinator marks derive `od_date` from `CampusDate(now)` and it is never
// accepted from the client. Teacher/admin search-marks are the CORRECTION path:
// they are exempt from slot windows and legitimately happen the morning after, so
// an unclamped campus date can land OUTSIDE the range — and T10's database
// trigger would then reject a correction the teacher is entitled to make.
//
// For a single-day event the clamp reproduces today's observable behaviour
// exactly: the record files under the event's date, which is what the public
// dashboard already shows.
//
// 0.12b REPLACES this default with an explicit teacher-chosen day. The clamp is a
// default, not a policy about which day is correct.
func TestClampToEventRange_PullsAnOutOfRangeDayToTheNearestBound(t *testing.T) {
	start := campusMidnight(t, 2026, time.August, 10)
	end := campusMidnight(t, 2026, time.September, 8)

	for _, tc := range []struct {
		name string
		day  time.Time
		want string
	}{
		{"before the range clamps to the first day", campusMidnight(t, 2026, time.August, 1), "2026-08-10"},
		{"after the range clamps to the last day", campusMidnight(t, 2026, time.September, 20), "2026-09-08"},
		{"inside the range is left alone", campusMidnight(t, 2026, time.August, 19), "2026-08-19"},
		{"the first day is inside, not before", campusMidnight(t, 2026, time.August, 10), "2026-08-10"},
		{"the last day is inside, not after", campusMidnight(t, 2026, time.September, 8), "2026-09-08"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ClampToEventRange(tc.day, start, end)
			if got.Format("2006-01-02") != tc.want {
				t.Errorf("[P0] ClampToEventRange(%s) = %s, want %s",
					tc.day.Format("2006-01-02"), got.Format("2006-01-02"), tc.want)
			}
		})
	}
}

// 0.12a-UNIT-006 — for a single-day event the clamp collapses to that one day,
// whatever the campus date is.
//
// This is the compatibility promise for the teacher/admin path specifically. A
// teacher correcting a mark three days after a one-day event must still be able
// to: that write succeeds, and it must never start failing on a range guard.
func TestClampToEventRange_SingleDayEventAlwaysFilesUnderThatDay(t *testing.T) {
	day := campusMidnight(t, 2026, time.August, 3)

	for _, when := range []time.Time{
		campusMidnight(t, 2026, time.August, 1),
		campusMidnight(t, 2026, time.August, 3),
		campusMidnight(t, 2026, time.August, 6),
	} {
		if got := ClampToEventRange(when, day, day); got.Format("2006-01-02") != "2026-08-03" {
			t.Errorf("[P0] a teacher correcting a one-day event on %s filed under %s, want 2026-08-03. "+
				"Today that correction succeeds; it must not start failing the range guard",
				when.Format("2006-01-02"), got.Format("2006-01-02"))
		}
	}
}
