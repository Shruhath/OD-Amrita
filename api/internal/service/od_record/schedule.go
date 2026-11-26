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
	// ── EVENING SLOTS, admitted only when extended slots are switched on ─────
	//
	// The teaching day ends at 18:15, so after that no slot is open and nobody
	// can be marked at all. These exist so the real marking flow can be
	// rehearsed in the evening, against the real stack, before an event.
	//
	// FORTY MINUTES EACH, and they stop at 23:35 rather than continuing the
	// 50-minute cadence to midnight. A slot that spanned midnight would open on
	// one campus day and close on the next, and `od_date` is a campus day — so
	// a mark taken at 00:10 inside a slot that began at 23:50 would be filed
	// under a day the slot did not belong to. Keeping every window inside one
	// day means no path has to reason about that at all.
	{slot: 13, startHour: 18, startMinute: 15, endHour: 18, endMinute: 55},
	{slot: 14, startHour: 18, startMinute: 55, endHour: 19, endMinute: 35},
	{slot: 15, startHour: 19, startMinute: 35, endHour: 20, endMinute: 15},
	{slot: 16, startHour: 20, startMinute: 15, endHour: 20, endMinute: 55},
	{slot: 17, startHour: 20, startMinute: 55, endHour: 21, endMinute: 35},
	{slot: 18, startHour: 21, startMinute: 35, endHour: 22, endMinute: 15},
	{slot: 19, startHour: 22, startMinute: 15, endHour: 22, endMinute: 55},
	{slot: 20, startHour: 22, startMinute: 55, endHour: 23, endMinute: 35},
}

const (
	// StandardSlotCount is the teaching day: slots 1..12, ending at 18:15. This
	// is what production runs on and what the extended-slots switch defaults to.
	StandardSlotCount = 12
	// ExtendedSlotCount is the ceiling the DATABASE allows (migration 0019).
	// Nothing may accept a slot above it, switch or no switch, because the CHECK
	// constraint would refuse the write and the caller would get an opaque 500
	// where they should have had a typed validation error.
	ExtendedSlotCount = 20
)

// SlotSpecTimes is one slot's configured window, in campus wall-clock parts.
//
// Exported so a test can assert the SHAPE of the schedule — that the evening
// slots are contiguous and that none of them crosses midnight — without
// standing up a clock and probing ActiveSlot minute by minute.
type SlotSpecTimes struct {
	StartHour, StartMinute int
	EndHour, EndMinute     int
}

// SlotWindowFor returns a slot's configured window.
func SlotWindowFor(slot int) (SlotSpecTimes, bool) {
	for _, spec := range slotSpecs {
		if spec.slot == slot {
			return SlotSpecTimes{
				StartHour: spec.startHour, StartMinute: spec.startMinute,
				EndHour: spec.endHour, EndMinute: spec.endMinute,
			}, true
		}
	}
	return SlotSpecTimes{}, false
}

// SlotCount is the effective ceiling for a given setting.
//
// One function rather than a bare conditional at each call site: "how many slots
// are there right now" is asked by validation, by the active-slot scan and by
// the screens, and three copies of that ternary is three places for it to drift.
func SlotCount(extendedEnabled bool) int {
	if extendedEnabled {
		return ExtendedSlotCount
	}
	return StandardSlotCount
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
// right. When the teacher picks a day (MarkInput.ODDate), that day is used
// instead and this default does not apply.
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
// `maxSlot` is the effective ceiling — SlotCount(...) — and is passed in rather
// than read from a package global so that a request's answer cannot depend on
// state some other request happened to set. Anything above it is simply not
// scanned, so with the switch off this behaves exactly as it did before the
// evening slots existed: after 18:15 there is no open slot.
func ActiveSlot(now time.Time, eventStart, eventEnd time.Time, maxSlot int) (int, SlotWindow, bool) {
	localNow := now.In(campusTZ)
	today := CampusDate(now)
	if today.Before(CampusDate(eventStart)) || today.After(CampusDate(eventEnd)) {
		return 0, SlotWindow{}, false
	}
	ty, tm, td := today.Date()
	for _, spec := range slotSpecs {
		if spec.slot > maxSlot {
			continue
		}
		start := time.Date(ty, tm, td, spec.startHour, spec.startMinute, 0, 0, campusTZ)
		end := time.Date(ty, tm, td, spec.endHour, spec.endMinute, 0, 0, campusTZ)
		if !localNow.Before(start) && localNow.Before(end) {
			window := SlotWindow{Slot: spec.slot, Start: start, End: end}
			return spec.slot, window, true
		}
	}
	return 0, SlotWindow{}, false
}
