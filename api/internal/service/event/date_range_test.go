// Red-phase unit tests for the event's date RANGE.
//
// COMPILE-RED on purpose: `CreateInput` gains `EndDate *string` and `CreateWrite`
// gains `EndDate time.Time`, so this file does not build until they exist.
//
// PRESCRIBED SURFACE:
//
//	type CreateInput struct { Name, EventDate string; EndDate *string; BranchIDs []string }
//	type CreateWrite struct { Name string; EventDate, EndDate time.Time; BranchIDs []uuid.UUID }
//	type Event      struct { …; EndDate string; … }
//
// `EndDate` is a POINTER on the input because oapi-codegen emits a non-required
// property as a pointer, and because nil has to be distinguishable from "" — an
// omitted end date defaults to the start date (AC 2), while an empty string is a
// client that sent the key with nothing in it and is a validation failure. A
// value field would collapse those two into one and silently create a one-day
// event for a malformed request.
//
// WHAT IS DELIBERATELY NOT HERE: the ORDERING rule (`end_date >= event_date`).
// T5 puts the ordering in the DATABASE as `events_date_range_ck` precisely
// because it must never be tunable, and it is asserted there by
// 0.12a-DB-001 against a real Postgres. Asserting it a second time here would be
// duplicate coverage of a rule this layer does not own.
//
// Reuses fakeReader / fakeWriter / fakeAuditStore from service_test.go — one set
// of fakes for the package, not a second parallel set.
package event

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

// endDate wraps a literal so a test can say `EndDate: endDate("2026-09-08")`.
// Its absence is `nil`, which is the shape every client written before
// end_date existed sends.
func endDate(value string) *string { return &value }

// `adminActor()` comes from category_test.go — one admin principal for the
// package, not a second parallel one.

// 0.12a-EVENT-001 — the 90-day bound is INCLUSIVE, and 91 is the first refusal.
//
// T5 puts this in the service rather than in a database CHECK because the bound
// is policy that will be tuned, and a CHECK would cost a migration each time it
// moved. Both sides are asserted because a bound written as `< 90` or as
// `end - start <= 90` (forgetting the inclusive +1) is off by one, and an
// off-by-one here is invisible until someone creates the one event that sits on
// the boundary.
//
// The refusal must also happen BEFORE any write boundary is touched, in the shape
// every other validation in this service already follows.
func TestCreate_RangeLengthBoundIsNinetyDaysInclusive(t *testing.T) {
	branchID := uuid.New()

	for _, tc := range []struct {
		name     string
		start    string
		end      string
		accepted bool
	}{
		{name: "a single day", start: "2026-08-10", end: "2026-08-10", accepted: true},
		{name: "exactly 90 days inclusive", start: "2026-08-10", end: "2026-11-07", accepted: true},
		{name: "91 days is the first refusal", start: "2026-08-10", end: "2026-11-08", accepted: false},
		{name: "a full year", start: "2026-08-10", end: "2027-08-09", accepted: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eventID := uuid.New()
			reader := &fakeReader{
				branches: []Branch{{ID: branchID.String()}},
				event:    Event{ID: eventID.String(), Name: "Amritotsavam", EventDate: tc.start, EndDate: tc.end},
			}
			writer := &fakeWriter{createID: eventID, createChanged: true}
			audit := &fakeAuditStore{}

			_, err := NewService(reader, audit, writer).Create(context.Background(), adminActor(), CreateInput{
				Name:      "Amritotsavam",
				EventDate: tc.start,
				EndDate:   endDate(tc.end),
				BranchIDs: []string{branchID.String()},
			})

			if tc.accepted {
				if err != nil {
					t.Fatalf("[P0] %s (%s..%s) was refused: %v. The bound is 90 days INCLUSIVE — "+
						"(end_date - event_date) + 1 <= 90", tc.name, tc.start, tc.end, err)
				}
				return
			}

			var de domain.DomainError
			if !errors.As(err, &de) || de.Code != contracts.ErrorCodeVALIDATIONFAILED {
				t.Fatalf("[P0] %s (%s..%s) error = %v, want VALIDATION_FAILED", tc.name, tc.start, tc.end, err)
			}
			if len(de.Details) != 1 || de.Details[0].Field != "end_date" || de.Details[0].Code != "range_too_long" {
				t.Fatalf("[P0] details = %#v, want end_date/range_too_long. The field must be `end_date` and not "+
					"`event_date`: the admin's start date is fine and the form has to mark the control that is "+
					"actually wrong", de.Details)
			}
			if audit.calls != 0 {
				t.Errorf("[P0] the write boundary was opened for an over-long range (%d calls). Every other "+
					"validation in this service refuses before the transaction; a refusal that has already "+
					"started one is a rollback where none was needed", audit.calls)
			}
		})
	}
}

// 0.12a-EVENT-002 — an omitted `end_date` yields a ONE-DAY event, not an error.
//
// This is AC 2 at the API boundary, and it is what literally satisfies "an event
// created with a single day behaves exactly as before". `CreateEventRequest.end_date`
// is not in `required`, so oapi-codegen hands the handler a nil pointer for every
// existing client — and nil must mean "same as the start date".
//
// The assertion is on what reaches the WRITER, not on what comes back, because
// the reader is a fake: it is the persisted value that matters, and a default
// applied only on the read path would leave a NULL in the database.
func TestCreate_OmittedEndDateDefaultsToTheStartDate(t *testing.T) {
	branchID, eventID := uuid.New(), uuid.New()
	reader := &fakeReader{
		branches: []Branch{{ID: branchID.String()}},
		event:    Event{ID: eventID.String(), Name: "Innovation Day", EventDate: "2026-08-15", EndDate: "2026-08-15"},
	}
	writer := &fakeWriter{createID: eventID, createChanged: true}
	audit := &fakeAuditStore{}

	if _, err := NewService(reader, audit, writer).Create(context.Background(), adminActor(), CreateInput{
		Name:      "Innovation Day",
		EventDate: "2026-08-15",
		EndDate:   nil, // the shape EVERY existing client sends
		BranchIDs: []string{branchID.String()},
	}); err != nil {
		t.Fatalf("[P0] omitting end_date was refused: %v. It is NOT in `required` — every client written before "+
			"end_date existed omits it, and requiring it turns a backwards-compatible addition into a breaking change", err)
	}

	if got := writer.createInput.EndDate.Format("2006-01-02"); got != "2026-08-15" {
		t.Errorf("[P0] persisted end_date = %q, want 2026-08-15 (the start date). A default applied only on the "+
			"READ path leaves a NULL in the database, and od_date range containment against a NULL end_date is "+
			"silently false for the whole event", got)
	}
	if got := writer.createInput.EventDate.Format("2006-01-02"); got != "2026-08-15" {
		t.Errorf("[P0] persisted event_date = %q, want 2026-08-15", got)
	}
}

// 0.12a-EVENT-003 — a malformed `end_date` is a VALIDATION_FAILED on `end_date`,
// never a nil-deref and never a silently-defaulted one-day event.
//
// T17 mirrors the raw-shape guard already at handler/event/handler.go:155-183: a
// JSON null, a non-string, or a string that does not round-trip through
// "2006-01-02" is a validation failure. An earlier code review found exactly this bug
// once — a value field turning an absent or malformed input into a zero value
// that parsed and reached Postgres.
//
// The empty-string case is the sharp one. `nil` means omitted and defaults to the
// start date; `""` means the client sent the key with nothing in it. Collapsing
// the two silently creates a one-day event for a request that was wrong.
func TestCreate_MalformedEndDateIsRefusedRatherThanDefaulted(t *testing.T) {
	branchID := uuid.New()

	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "an empty string is not an omission", value: ""},
		{name: "a date that does not exist", value: "2026-02-31"},
		{name: "the wrong format", value: "15-08-2026"},
		{name: "a timestamp rather than a date", value: "2026-08-15T00:00:00Z"},
		{name: "free text", value: "next friday"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			audit := &fakeAuditStore{}
			_, err := NewService(&fakeReader{branches: []Branch{{ID: branchID.String()}}}, audit, &fakeWriter{}).
				Create(context.Background(), adminActor(), CreateInput{
					Name:      "Event",
					EventDate: "2026-08-15",
					EndDate:   endDate(tc.value),
					BranchIDs: []string{branchID.String()},
				})

			var de domain.DomainError
			if !errors.As(err, &de) || de.Code != contracts.ErrorCodeVALIDATIONFAILED {
				t.Fatalf("[P0] end_date = %q was accepted or mis-mapped: %v. Want VALIDATION_FAILED", tc.value, err)
			}
			if len(de.Details) != 1 || de.Details[0].Field != "end_date" || de.Details[0].Code != "invalid_format" {
				t.Fatalf("[P0] details = %#v, want end_date/invalid_format", de.Details)
			}
			if audit.calls != 0 {
				t.Errorf("[P0] the write boundary was opened for a malformed end_date")
			}
		})
	}
}

// 0.12a-EVENT-004 — the created event's RANGE is in the audit payload.
//
// T5/T17. The create audit row already carries `event_date` and `branch_ids`;
// the range is now half the fact being recorded, and an audit trail that records
// a thirty-day festival as its first day cannot answer "what did the admin
// actually create". NFR20 keeps the row and the write in one transaction, so this
// is an addition to the existing payload, never a second audit row.
func TestCreate_AuditPayloadCarriesTheWholeRange(t *testing.T) {
	branchID, eventID := uuid.New(), uuid.New()
	reader := &fakeReader{
		branches: []Branch{{ID: branchID.String()}},
		event:    Event{ID: eventID.String(), Name: "Amritotsavam", EventDate: "2026-08-10", EndDate: "2026-09-08"},
	}
	writer := &fakeWriter{createID: eventID, createChanged: true}
	audit := &fakeAuditStore{}

	if _, err := NewService(reader, audit, writer).Create(context.Background(), adminActor(), CreateInput{
		Name:      "Amritotsavam",
		EventDate: "2026-08-10",
		EndDate:   endDate("2026-09-08"),
		BranchIDs: []string{branchID.String()},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if audit.calls != 1 {
		t.Fatalf("[P0] audit calls = %d, want exactly 1 — the range is an addition to the EXISTING create audit "+
			"row, not a second one", audit.calls)
	}

	var payload map[string]any
	if err := json.Unmarshal(audit.event.Payload, &payload); err != nil {
		t.Fatalf("decode audit payload %s: %v", audit.event.Payload, err)
	}
	for field, want := range map[string]string{"event_date": "2026-08-10", "end_date": "2026-09-08"} {
		got, ok := payload[field].(string)
		if !ok {
			t.Errorf("[P0] audit payload has no %s. Payload: %s", field, audit.event.Payload)
			continue
		}
		if got != want {
			t.Errorf("[P0] audit payload %s = %q, want %q", field, got, want)
		}
	}
	if _, leaked := payload["series_id"]; leaked {
		t.Errorf("[P0] audit payload carries a series_id. The event-SERIES approach was REVERSED on 2026-08-06: " +
			"one festival is ONE row with a real date range")
	}
}

// 0.12a-EVENT-005 — exactly ONE event row is written per create, whatever the
// range length.
//
// The cheapest possible guard against building the superseded option. An earlier
// revision of the plan recommended thirty events sharing a `series_id` plus a
// bulk-create endpoint, and a fan-out would show up here as N calls into the
// writer. That is the first anti-pattern to rule out; this is the assertion
// that makes it a test rather than a wish.
func TestCreate_AThirtyDayRangeWritesExactlyOneEventRow(t *testing.T) {
	branchID, eventID := uuid.New(), uuid.New()
	reader := &fakeReader{
		branches: []Branch{{ID: branchID.String()}},
		event:    Event{ID: eventID.String(), Name: "Amritotsavam", EventDate: "2026-08-10", EndDate: "2026-09-08"},
	}
	writer := &countingCreateWriter{fakeWriter: fakeWriter{createID: eventID, createChanged: true}}

	if _, err := NewService(reader, &fakeAuditStore{}, writer).Create(context.Background(), adminActor(), CreateInput{
		Name:      "Amritotsavam",
		EventDate: "2026-08-10",
		EndDate:   endDate("2026-09-08"),
		BranchIDs: []string{branchID.String()},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if writer.creates != 1 {
		t.Errorf("[P0] Create wrote %d event rows for a 30-day range, want exactly 1. A fan-out that writes one "+
			"row per day is the event-SERIES option, which was REVERSED on 2026-08-06 — one festival is one row "+
			"with one set of categories and one set of coordinator assignments", writer.creates)
	}
	if got := writer.createInput.EndDate.Format("2006-01-02"); got != "2026-09-08" {
		t.Errorf("[P0] the single row's end_date = %q, want 2026-09-08", got)
	}
}

// countingCreateWriter counts Create calls so a fan-out is visible as a number
// rather than inferred from the last input recorded.
type countingCreateWriter struct {
	fakeWriter
	creates int
}

func (w *countingCreateWriter) Create(ctx context.Context, tx pgx.Tx, input CreateWrite) (uuid.UUID, bool, error) {
	w.creates++
	return w.fakeWriter.Create(ctx, tx, input)
}
