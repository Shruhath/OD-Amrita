// Package campus holds the one definition of the campus wall clock.
//
// It exists because two service packages need the same answer to "what day is it
// on campus right now?" — the marking path, to decide the day an OD belongs to,
// and the event admin path, to decide the day a fallback-credit pool is read or
// reset for. Making one import the other to ask would point the event admin
// service at the marking service for a calendar question; copying the conversion
// into both would put the campus offset in two places, and the offset is exactly
// the thing that must not be reimplemented.
//
// Asia/Kolkata is +05:30 with no DST. The half hour is the whole hazard: a
// hand-rolled +5h truncation misfiles every instant between 00:00 and 00:30
// campus time under the previous day, every night, silently.
package campus

import "time"

// TZ is the campus location. Resolved once at init: a per-call
// time.LoadLocation would put a filesystem read on the mark path.
var TZ = mustLocation()

func mustLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		// A binary that cannot resolve the campus timezone cannot decide which
		// day any OD belongs to. Failing at startup is the only honest option;
		// falling back to UTC would misfile marks for five and a half hours a day.
		panic(err)
	}
	return loc
}

// Date returns campus-local midnight for an instant — the day something
// happening at that moment belongs to.
func Date(now time.Time) time.Time {
	local := now.In(TZ)
	y, m, d := local.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, TZ)
}
