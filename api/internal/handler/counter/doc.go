// Package counter is an empty reserved directory. The marking counters shipped
// elsewhere: they are served by GET /api/v1/mark/status, whose handler lives in
// handler/od_record/marking_reads.go beside the mark handler, because the
// counters and the slot window are one response and share its scope check.
//
// Nothing is expected to land here. Delete the directory if it is still empty
// when the attendance-view work needs a home.
package counter
