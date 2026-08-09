package marking

import "time"

// AttendanceActor is the display snapshot for who marked an OD record.
type AttendanceActor struct {
	UserID *string
	Email  *string
	Name   *string
}

// AttendanceRecord is the admin attendance view of one present OD row.
type AttendanceRecord struct {
	RecordID string
	Student  Student
	EventID  string
	Category Category
	Slot     int
	// ODDate is YYYY-MM-DD — the day the OD belongs to. ServerReceivedAt is when
	// the row was written, and on a correction the two are different days.
	ODDate           string
	CaptureMethod    string
	Status           string
	ReasonNote       *string
	MarkedBy         AttendanceActor
	ActorRole        string
	ServerReceivedAt time.Time
}

// AttendancePage is the paginated attendance collection.
type AttendancePage struct {
	Data     []AttendanceRecord
	Page     int
	PageSize int
	Total    int
}

// AttendanceFilters are the optional server-side filters used by the record
// management view. They intentionally do not include status: that view must
// show both present and removed rows so an auditor cannot mistake a filtered
// absence for an erased history.
type AttendanceFilters struct {
	CategoryID  *string
	Slot        *int
	ODDate      *string
	Section     *string
	ActorUserID *string
}
