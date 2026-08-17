package odrecord

// combined_reader_test.go — the one-round-trip event+category lookup must be
// INDISTINGUISHABLE from the two-query path it replaces.
//
// The mark path resolved the event and then verified its category as two
// sequential queries, on a path that already makes several against a database
// roughly half a second away. Collapsing them is worth doing and is exactly the
// kind of change that quietly alters which error a client sees, so both paths
// are exercised against the same table of cases here.

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recordingReader struct {
	event        Event
	eventErr     error
	categoryErr  error
	eventCalls   int
	catCalls     int
	combined     bool
	combineCalls int
}

func (r *recordingReader) EventForMark(context.Context, string) (Event, error) {
	r.eventCalls++
	return r.event, r.eventErr
}

func (r *recordingReader) CategoryForEvent(context.Context, string, string) (Category, error) {
	r.catCalls++
	if r.categoryErr != nil {
		return Category{}, r.categoryErr
	}
	return Category{}, nil
}

func (r *recordingReader) StudentForMark(context.Context, string) (Student, error) {
	return Student{StudentID: "student-1", RollNo: "AM001", DisplayName: "Person"}, nil
}

// combinedReader adds the optional interface. It mirrors SQLCReader's contract:
// the event comes back even alongside ErrCategoryNotFound.
type combinedReader struct{ *recordingReader }

func (r combinedReader) EventWithCategoryForMark(context.Context, string, string) (Event, error) {
	r.combineCalls++
	if r.eventErr != nil {
		return Event{}, r.eventErr
	}
	if r.categoryErr != nil {
		return r.event, r.categoryErr
	}
	return r.event, nil
}

func activeEvent() Event {
	day := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	return Event{EventID: "event-1", EventDate: day, EndDate: day, Status: "active"}
}

func TestEventWithCategory_BothPathsAgree(t *testing.T) {
	archived := activeEvent()
	archived.Status = "archived"

	cases := []struct {
		name        string
		event       Event
		eventErr    error
		categoryErr error
		wantErr     error
		wantEvent   bool // the event must come back even on a category error
	}{
		{name: "both resolve", event: activeEvent(), wantEvent: true},
		{name: "event missing", eventErr: ErrEventNotFound, wantErr: ErrEventNotFound},
		{
			name: "category not in event", event: activeEvent(),
			categoryErr: ErrCategoryNotFound, wantErr: ErrCategoryNotFound, wantEvent: true,
		},
		{
			// PRECEDENCE. An archived event with a bad category must still report
			// the event as archived, which the caller can only decide if the event
			// is in hand alongside the category error.
			name: "archived event with a bad category", event: archived,
			categoryErr: ErrCategoryNotFound, wantErr: ErrCategoryNotFound, wantEvent: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := &recordingReader{event: tc.event, eventErr: tc.eventErr, categoryErr: tc.categoryErr}
			split := &Service{reader: base}

			combinedBase := &recordingReader{event: tc.event, eventErr: tc.eventErr, categoryErr: tc.categoryErr}
			combined := &Service{reader: combinedReader{recordingReader: combinedBase}}

			splitEvent, splitErr := split.eventWithCategory(context.Background(), "event-1", "category-1")
			combinedEvent, combinedErr := combined.eventWithCategory(context.Background(), "event-1", "category-1")

			if !errors.Is(splitErr, tc.wantErr) {
				t.Errorf("two-query path: err = %v, want %v", splitErr, tc.wantErr)
			}
			if !errors.Is(combinedErr, tc.wantErr) {
				t.Errorf("combined path: err = %v, want %v", combinedErr, tc.wantErr)
			}
			if splitEvent != combinedEvent {
				t.Errorf("paths disagree on the event: %+v vs %+v", splitEvent, combinedEvent)
			}
			if tc.wantEvent && combinedEvent.EventID == "" {
				t.Error("the event must come back even alongside a category error, or the archived answer loses its precedence")
			}
		})
	}
}

// The collapse is the point: a reader that offers the combined lookup must not
// also be asked the two separate questions.
func TestEventWithCategory_CombinedReaderMakesOneCall(t *testing.T) {
	base := &recordingReader{event: activeEvent()}
	svc := &Service{reader: combinedReader{recordingReader: base}}

	if _, err := svc.eventWithCategory(context.Background(), "event-1", "category-1"); err != nil {
		t.Fatalf("eventWithCategory: %v", err)
	}

	if base.eventCalls != 0 || base.catCalls != 0 {
		t.Errorf("fell back to the split path: %d event calls, %d category calls, want 0 and 0",
			base.eventCalls, base.catCalls)
	}
}

// A reader without the optional interface must keep working exactly as before.
func TestEventWithCategory_PlainReaderStillUsesBothQueries(t *testing.T) {
	base := &recordingReader{event: activeEvent()}
	svc := &Service{reader: base}

	if _, err := svc.eventWithCategory(context.Background(), "event-1", "category-1"); err != nil {
		t.Fatalf("eventWithCategory: %v", err)
	}

	if base.eventCalls != 1 || base.catCalls != 1 {
		t.Errorf("%d event calls, %d category calls, want 1 and 1", base.eventCalls, base.catCalls)
	}
}
