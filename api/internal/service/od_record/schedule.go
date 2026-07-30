package odrecord

import "time"

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

var campusTZ = mustCampusLocation()

func mustCampusLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		panic(err)
	}
	return loc
}

func ActiveSlot(now time.Time, eventDate time.Time) (int, SlotWindow, bool) {
	localNow := now.In(campusTZ)
	localEvent := eventDate.In(campusTZ)
	ny, nm, nd := localNow.Date()
	ey, em, ed := localEvent.Date()
	if ny != ey || nm != em || nd != ed {
		return 0, SlotWindow{}, false
	}
	for _, spec := range slotSpecs {
		start := time.Date(ey, em, ed, spec.startHour, spec.startMinute, 0, 0, campusTZ)
		end := time.Date(ey, em, ed, spec.endHour, spec.endMinute, 0, 0, campusTZ)
		if !localNow.Before(start) && localNow.Before(end) {
			window := SlotWindow{Slot: spec.slot, Start: start, End: end}
			return spec.slot, window, true
		}
	}
	return 0, SlotWindow{}, false
}
