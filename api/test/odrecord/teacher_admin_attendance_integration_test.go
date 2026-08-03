//go:build integration

package odrecordtest

import (
	"os"
	"testing"
)

func requireTeacherAdminAttendanceIntegrationATDD(t *testing.T) {
	t.Helper()
	if os.Getenv("ATDD_ACTIVE_09") != "1" {
		t.Skip("red-phase scaffold for 0.9; set ATDD_ACTIVE_09=1 while implementing")
	}
}

func TestTeacherAdminSearchMarkIntegration_AuditConservationAndNoCreditDebit(t *testing.T) {
	requireTeacherAdminAttendanceIntegrationATDD(t)
	t.Fatalf("[P0] scaffold: seed a branch-associated teacher, POST search+slot through od_record.Service, then assert exactly one od_records row, one od.created audit row, actor_role=teacher, capture_method=search, and no capture-credit debit")
}

func TestTeacherAdminSearchMarkIntegration_DuplicateConcurrentMarksConserveRows(t *testing.T) {
	requireTeacherAdminAttendanceIntegrationATDD(t)
	t.Fatalf("[P0] scaffold: two concurrent teacher/admin search marks for the same student/event/slot must yield one success, one SCAN_DUPLICATE, one od_records row and one od.created audit row")
}

func TestAdminAttendanceIntegration_FiltersBeforePagination(t *testing.T) {
	requireTeacherAdminAttendanceIntegrationATDD(t)
	t.Fatalf("[P0] scaffold: seed records across two events/categories/slots, read attendance for one event with category/slot filters, and assert pagination total is computed after filters with no cross-event rows")
}

func TestAdminAttendanceIntegration_NullActorStillRendersRecord(t *testing.T) {
	requireTeacherAdminAttendanceIntegrationATDD(t)
	t.Fatalf("[P1] scaffold: mark with an actor, null or delete the actor reference, and assert the attendance row remains readable with nullable marked_by fields")
}
