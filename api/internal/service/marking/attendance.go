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
	RecordID         string
	Student          Student
	EventID          string
	Category         Category
	Slot             int
	CaptureMethod    string
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
