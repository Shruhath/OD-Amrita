package publiccache

import (
	"context"
	"testing"
	"time"

	"github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
	svcstudent "github.com/Shruhath/OD-Amrita/api/internal/service/student"
)

type countingRecords struct {
	calls map[string]int
}

func (r *countingRecords) Records(_ context.Context, studentID string) (publicattendance.RecordCollection, error) {
	if r.calls == nil {
		r.calls = map[string]int{}
	}
	r.calls[studentID]++
	return publicattendance.RecordCollection{
		Student: publicattendance.Student{StudentID: studentID, RollNo: "ROLL-" + studentID},
	}, nil
}

type countingEvents struct{ calls int }

func (e *countingEvents) PublicEvents(context.Context) (publicattendance.EventCollection, error) {
	e.calls++
	return publicattendance.EventCollection{}, nil
}

type countingSearch struct{ calls map[string]int }

func (s *countingSearch) PublicSearch(_ context.Context, query string) ([]svcstudent.SearchResult, error) {
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[query]++
	return []svcstudent.SearchResult{{RollNo: query}}, nil
}

func TestRepeatedRecordReadsHitTheDatabaseOnce(t *testing.T) {
	inner := &countingRecords{}
	cached := NewRecords(inner, time.Minute)

	for range 10 {
		if _, err := cached.Records(context.Background(), "student-1"); err != nil {
			t.Fatal(err)
		}
	}
	if inner.calls["student-1"] != 1 {
		t.Fatalf("the database was read %d times, want 1", inner.calls["student-1"])
	}
}

// The defect a per-student cache can produce, and the one that matters most on
// this surface: showing one student another student's attendance.
func TestOneStudentIsNeverServedAnotherStudentsRecords(t *testing.T) {
	inner := &countingRecords{}
	cached := NewRecords(inner, time.Minute)

	first, err := cached.Records(context.Background(), "student-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := cached.Records(context.Background(), "student-2")
	if err != nil {
		t.Fatal(err)
	}
	if first.Student.StudentID != "student-1" || second.Student.StudentID != "student-2" {
		t.Fatalf("cache crossed students: got %q and %q", first.Student.StudentID, second.Student.StudentID)
	}
}

func TestDistinctSearchQueriesAreNotCollapsed(t *testing.T) {
	inner := &countingSearch{}
	cached := NewSearch(inner, time.Minute)

	first, err := cached.PublicSearch(context.Background(), "CB.SC.U4CSE99101")
	if err != nil {
		t.Fatal(err)
	}
	second, err := cached.PublicSearch(context.Background(), "CB.SC.U4CSE99102")
	if err != nil {
		t.Fatal(err)
	}
	if first[0].RollNo == second[0].RollNo {
		t.Fatal("two different roll-number searches returned the same results")
	}
}

func TestTheEventListIsLoadedOncePerLifetime(t *testing.T) {
	inner := &countingEvents{}
	cached := NewEvents(inner, time.Minute)

	for range 10 {
		if _, err := cached.PublicEvents(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("the event list was read %d times, want 1", inner.calls)
	}
}

// Zero is the documented off switch, and it has to reach all the way through
// the decorator — a decorator that quietly cached anyway would make the operator
// escape hatch a lie on the one day it is reached for.
func TestAZeroLifetimeReadsEveryTime(t *testing.T) {
	inner := &countingRecords{}
	cached := NewRecords(inner, 0)

	for range 4 {
		if _, err := cached.Records(context.Background(), "student-1"); err != nil {
			t.Fatal(err)
		}
	}
	if inner.calls["student-1"] != 4 {
		t.Fatalf("the database was read %d times, want 4 — a zero lifetime still cached", inner.calls["student-1"])
	}
}
