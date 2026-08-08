package odrecord

import (
	"time"

	"github.com/Shruhath/OD-Amrita/api/internal/campus"
)

type SlotWindow struct {
	Slot  int
	Start time.Time
	End   time.Time
}

type slotSpec struct {
	slot        int
	startHour   int
	startMinute int
	endHour     int
	endMinute   int
}

var slotSpecs = []slotSpec{
	{slot: 1, startHour: 8, startMinute: 0, endHour: 8, endMinute: 50},
	{slot: 2, startHour: 8, startMinute: 50, endHour: 9, endMinute: 40},
	{slot: 3, startHour: 9, startMinute: 40, endHour: 10, endMinute: 30},
	{slot: 4, startHour: 10, startMinute: 45, endHour: 11, endMinute: 35},
	{slot: 5, startHour: 11, startMinute: 35, endHour: 12, endMinute: 25},
	{slot: 6, startHour: 12, startMinute: 25, endHour: 13, endMinute: 15},
	{slot: 7, startHour: 13, startMinute: 15, endHour: 14, endMinute: 5},
	{slot: 8, startHour: 14, startMinute: 5, endHour: 14, endMinute: 55},
	{slot: 9, startHour: 14, startMinute: 55, endHour: 15, endMinute: 45},
	{slot: 10, startHour: 15, startMinute: 45, endHour: 16, endMinute: 35},
	{slot: 11, startHour: 16, startMinute: 35, endHour: 17, endMinute: 25},
	{slot: 12, startHour: 17, startMinute: 25, endHour: 18, endMinute: 15},
}

var campusTZ = campus.TZ

// CampusDate returns campus-local midnight for an instant — the day an OD taken
// at that moment belongs to.
//
// Deliberately not a return value of ActiveSlot: the credit debit and both
// counters need the day even when no slot is open, and folding it in would make
// those paths ask a question about slots in order to learn the date.
//
// Asia/Kolkata is +05:30 with no DST. The half hour is the reason this is a
// function rather than an inline truncation: a naive +5h offset misfiles every
// mark taken between 00:00 and 00:30 campus time under the previous day.
func CampusDate(now time.Time) time.Time { return campus.Date(now) }

// ClampToEventRange pulls a day inside an event's range, to whichever bound it
// overshot.
//
// This is the teacher/admin default. Those writes are the CORRECTION path: they
// are exempt from slot windows and legitimately happen the morning after, so an
// unclamped campus date can land outside the range — and the database's range
// guard would then refuse a correction the teacher is entitled to make. For a
// one-day event the clamp collapses to that day, which reproduces today's
// observable behaviour exactly.
//
// It is a default about which day to assume, not a claim about which day is
// right. Story 0.12b replaces it with a day the teacher picks.
func ClampToEventRange(day, eventStart, eventEnd time.Time) time.Time {
	start := CampusDate(eventStart)
	end := CampusDate(eventEnd)
	candidate := CampusDate(day)
	if candidate.Before(start) {
		return start
	}
	if candidate.After(end) {
		return end
	}
	return candidate
}

// ActiveSlot reports the slot open right now, if today falls inside the event's
// range.
//
// The range is INCLUSIVE at both ends: the first and last day of a festival are
// precisely when marking happens, and an exclusive bound would make them
// unmarkable. A single-day event passes eventStart == eventEnd and behaves
// exactly as it did before the range existed.
//
// The slot scan runs against TODAY, not against the event's start date —
// anchoring the window to the start would hand every coordinator after day one a
// countdown that already expired.
func ActiveSlot(now time.Time, eventStart, eventEnd time.Time) (int, SlotWindow, bool) {
	localNow := now.In(campusTZ)
	today := CampusDate(now)
	if today.Before(CampusDate(eventStart)) || today.After(CampusDate(eventEnd)) {
		return 0, SlotWindow{}, false
	}
	ty, tm, td := today.Date()
	for _, spec := range slotSpecs {
		start := time.Date(ty, tm, td, spec.startHour, spec.startMinute, 0, 0, campusTZ)
		end := time.Date(ty, tm, td, spec.endHour, spec.endMinute, 0, 0, campusTZ)
		if !localNow.Before(start) && localNow.Before(end) {
			window := SlotWindow{Slot: spec.slot, Start: start, End: end}
			return spec.slot, window, true
		}
	}
	return 0, SlotWindow{}, false
}
