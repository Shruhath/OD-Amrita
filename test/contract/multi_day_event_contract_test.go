// Contract gates for the multi-day event date range.
//
// RED PHASE. These parse contracts/openapi.yaml as YAML — no generated Go types —
// so this file COMPILES today and fails on assertions that name the missing
// thing. A package that fails to build reports one opaque error for the whole
// suite, and the contract is the first artifact the dev touches.
//
// NOT gated behind a build tag or an env flag. Four files in this repo already
// sit behind `//go:build atdd` with nothing anywhere passing `-tags atdd`, which
// is the same defect as an unset ATDD_ACTIVE_09 env flag in a different disguise. A
// skipped test and a missing test are the same signal.
package contract

import (
	"slices"
	"testing"
)

// 0.12a-CONTRACT-001 — `Event.end_date` is REQUIRED, `CreateEventRequest.end_date`
// is OPTIONAL, and that asymmetry is load-bearing rather than an oversight.
//
// oapi-codegen v2 emits a required property as a VALUE and a non-required one as
// a POINTER. The server always has an end date (the migration backfills every
// pre-existing row to `end_date = event_date`), so `Event.end_date` must be a
// value — an optional key becomes an `| undefined` the web silently renders as
// blank, and the events list would show a range with a missing half.
//
// `CreateEventRequest.end_date` must NOT be required, because "an event created
// with a single day behaves exactly as before" (AC 2) is literally satisfied at
// the API boundary by letting the caller omit the field and defaulting it
// server-side. Making it required would break every existing client and turn a
// backwards-compatible addition into a breaking change.
func TestMultiDayEventContract_EndDateIsRequiredOnEventAndOptionalOnCreate(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	event := getMap(schemas, "Event")
	if event == nil {
		t.Fatal("[P0] components.schemas.Event is missing")
	}
	eventProps := getMap(event, "properties")
	if eventProps["end_date"] == nil {
		t.Errorf("[P0] Event.end_date is missing. The event is a RANGE now: `event_date` is the first day and " +
			"`end_date` is the last (T2 keeps the original name deliberately — do NOT rename it to start_date)")
	}
	if got := getStr(getMap(eventProps, "end_date"), "format"); got != "date" {
		t.Errorf("[P0] Event.end_date format = %q, want date", got)
	}
	if missing := missingRequired(event, "end_date"); len(missing) > 0 {
		t.Errorf("[P0] Event.required is missing %v. The server always has an end date, so this must generate a "+
			"VALUE and not a pointer; an optional key becomes an `| undefined` the UI renders as blank", missing)
	}

	create := getMap(schemas, "CreateEventRequest")
	if create == nil {
		t.Fatal("[P0] components.schemas.CreateEventRequest is missing")
	}
	createProps := getMap(create, "properties")
	if createProps["end_date"] == nil {
		t.Errorf("[P0] CreateEventRequest.end_date is missing — an admin has no way to declare a range")
	}
	if got := getStr(getMap(createProps, "end_date"), "format"); got != "date" {
		t.Errorf("[P0] CreateEventRequest.end_date format = %q, want date", got)
	}
	if len(missingRequired(create, "end_date")) == 0 {
		t.Errorf("[P0] CreateEventRequest.end_date is in `required`. It must stay OPTIONAL: omitting it is what " +
			"makes a single-day create behave exactly as before (AC 2), and oapi-codegen emits an optional " +
			"property as a POINTER — which is the shape handler/event's raw-shape guard is written against. " +
			"Making it required turns a backwards-compatible addition into a breaking change")
	}
	// The pre-existing required fields must survive the addition. A `required`
	// list rewritten rather than appended to is a silent relaxation.
	if missing := missingRequired(create, "name", "event_date", "branch_ids"); len(missing) > 0 {
		t.Errorf("[P0] CreateEventRequest lost required fields %v while gaining end_date", missing)
	}
}

// 0.12a-CONTRACT-002 — the public day group is keyed on the OD's date, and the
// public record carries BOTH dates because they answer different questions.
//
// `PublicODDay.event_date` is renamed to `od_date` (T18). The key must not lie
// about which fact it is: today it holds the EVENT's date because one event was
// one day, and with the date range a thirty-day festival must show as up to thirty
// day groups. Leaving the name would produce a field called `event_date` whose
// value is not the event's date — the exact confusion that makes a next-morning
// correction file under the wrong day.
//
// `PublicODRecord` KEEPS `event_date` and GAINS `od_date`. The record's
// `event_date` stays the event's FIRST day because `eventsFromRecords` in
// PublicAttendanceScreen.tsx builds the dispute form's event options from it; a
// record that reported its own day there would offer the student an event option
// dated to one arbitrary record.
func TestMultiDayEventContract_PublicDayIsKeyedOnTheODDate(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	day := getMap(schemas, "PublicODDay")
	if day == nil {
		t.Fatal("[P0] components.schemas.PublicODDay is missing")
	}
	dayProps := getMap(day, "properties")
	if dayProps["od_date"] == nil {
		t.Errorf("[P0] PublicODDay.od_date is missing — the grouping key must name the fact it holds (T18)")
	}
	if dayProps["event_date"] != nil {
		t.Errorf("[P0] PublicODDay still declares `event_date`. It is RENAMED, not supplemented: a day group " +
			"keyed on the event's start date collapses a thirty-day festival onto one heading, which reads to " +
			"the student as twenty-nine days of missing attendance")
	}
	if missing := missingRequired(day, "od_date", "records"); len(missing) > 0 {
		t.Errorf("[P0] PublicODDay.required is missing %v", missing)
	}

	record := getMap(schemas, "PublicODRecord")
	if record == nil {
		t.Fatal("[P0] components.schemas.PublicODRecord is missing")
	}
	recordProps := getMap(record, "properties")
	if recordProps["od_date"] == nil {
		t.Errorf("[P0] PublicODRecord.od_date is missing — the day the OD belongs to")
	}
	if recordProps["event_date"] == nil {
		t.Errorf("[P0] PublicODRecord.event_date was removed. It must STAY: it is the event's FIRST day and " +
			"eventsFromRecords() builds the dispute form's event options from it")
	}
	if missing := missingRequired(record, "od_date", "event_date"); len(missing) > 0 {
		t.Errorf("[P0] PublicODRecord.required is missing %v", missing)
	}
}

// 0.12a-CONTRACT-003 — the admin attendance read gains an `od_date` filter.
//
// T22. Without it a thirty-day event's attendance list interleaves thirty days of
// the same slot with nothing on screen distinguishing them, which is not a
// cosmetic shortfall: the whole purpose of that surface is verifying who was
// marked, and "slot 8" appearing thirty times is unreadable.
//
// The parameter must follow the shape of `category_id`/`slot` already there —
// optional, in query, `format: date`. It must NOT be required: the unfiltered
// read is still the default and every existing caller omits it.
func TestMultiDayEventContract_AdminAttendanceHasAnODDateFilter(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	op := getMap(getMap(getMap(doc, "paths"), adminAttendancePath), "get")
	if op == nil {
		t.Fatalf("[P0] missing admin attendance operation GET %s", adminAttendancePath)
	}

	params := indexParams(op)
	odDate := params["od_date"]
	if odDate == nil {
		t.Fatalf("[P0] GET %s has no `od_date` query parameter (T22). A thirty-day event's attendance list "+
			"otherwise interleaves thirty days of the same slot with nothing distinguishing them", adminAttendancePath)
	}
	if got := getStr(odDate, "in"); got != "query" {
		t.Errorf("[P0] od_date.in = %q, want query — it is a filter, not a path segment", got)
	}
	if required, _ := odDate["required"].(bool); required {
		t.Errorf("[P0] od_date is required. It must be OPTIONAL: the unfiltered read is the default and every " +
			"existing caller omits it, so requiring it breaks the surface it is extending")
	}
	if got := getStr(resolveSchema(doc, getMap(odDate, "schema")), "format"); got != "date" {
		t.Errorf("[P0] od_date schema format = %q, want date", got)
	}

	// The filters that already exist must survive. A parameter list rewritten
	// rather than appended to silently drops the two filters the surface has.
	for _, name := range []string{"category_id", "slot", "page", "page_size"} {
		if params[name] == nil {
			t.Errorf("[P0] GET %s lost parameter %s while gaining od_date", adminAttendancePath, name)
		}
	}
}

// 0.12a-CONTRACT-004 — `PublicEvent` carries the range, so the dispute form's day
// picker has something to enumerate.
//
// T19, and 0.11 left the instruction in `publicDisputeSchemaAllowlists` verbatim:
// *"PublicEvent gains `end_date` with the date range … Add it to this allowlist in
// the SAME change."* Without the range on the wire, `dayOptions` in
// PublicAttendanceScreen.tsx has exactly one day to offer and a student missed on
// day 17 of a festival cannot report it.
func TestMultiDayEventContract_PublicEventCarriesTheRange(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	publicEvent := getMap(getMap(getMap(doc, "components"), "schemas"), "PublicEvent")
	if publicEvent == nil {
		t.Fatal("[P0] components.schemas.PublicEvent is missing")
	}
	props := getMap(publicEvent, "properties")
	if props["end_date"] == nil {
		t.Errorf("[P0] PublicEvent.end_date is missing (T19). The dispute form's day picker enumerates " +
			"event_date..end_date; with no end_date it offers one option and a student missed on day 17 of a " +
			"festival has no way to report it")
	}
	if got := getStr(getMap(props, "end_date"), "format"); got != "date" {
		t.Errorf("[P0] PublicEvent.end_date format = %q, want date", got)
	}
	if missing := missingRequired(publicEvent, "end_date"); len(missing) > 0 {
		t.Errorf("[P0] PublicEvent.required is missing %v; an optional key becomes an `| undefined` and the day "+
			"picker silently falls back to a single option", missing)
	}
}

// 0.12a-CONTRACT-005 — no event SERIES surface appears anywhere in the spec.
//
// The plan's earlier revision recommended thirty events sharing a `series_id`
// plus a bulk-create endpoint, and it was REVERSED on 2026-08-06. One festival is
// ONE row. This is the cheapest possible guard against building the superseded
// option: a fan-out that writes N event rows has to surface somewhere in the
// contract, and this catches it before any of it is written.
//
// Deliberately a substring sweep over operation ids AND paths, in the shape of
// the deferred-surface guard in teacher_admin_attendance_contract_test.go.
func TestMultiDayEventContract_NoEventSeriesSurface(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))

	forbidden := []string{"series", "bulk", "batchcreateevent"}
	paths := getMap(doc, "paths")
	for path, raw := range paths {
		item := asMap(raw)
		for method := range item {
			id := getStr(getMap(item, method), "operationId")
			for _, token := range forbidden {
				if containsFold(id, token) || containsFold(path, token) {
					t.Errorf("[P0] %s %s (operationId %q) looks like an event SERIES surface. That option was "+
						"REVERSED on 2026-08-06: a multi-day event is ONE row with a real date range, not N rows "+
						"sharing a series_id", method, path, id)
				}
			}
		}
	}

	for name := range getMap(getMap(doc, "components"), "schemas") {
		for _, token := range forbidden {
			if containsFold(name, token) {
				t.Errorf("[P0] components.schemas.%s looks like an event SERIES schema; see above", name)
			}
		}
	}

	// And the range itself must never be expressible as a list of days. A
	// `skipped_days` / `dates[]` field is the exclusion list T5 rules out, and it
	// would reintroduce the fan-out through the back door.
	for _, schemaName := range []string{"Event", "CreateEventRequest", "PublicEvent"} {
		props := getMap(getMap(getMap(getMap(doc, "components"), "schemas"), schemaName), "properties")
		for name := range props {
			if slices.Contains([]string{"dates", "days", "skipped_days", "excluded_dates", "series_id"}, name) {
				t.Errorf("[P0] %s.%s enumerates days. The range is two dates (T5): every day between them is "+
					"markable, and a coordinator simply does not mark on a Sunday", schemaName, name)
			}
		}
	}
}

// The case-insensitive substring sweep reuses `containsFold` from
// marking_surface_contract_test.go rather than defining a second copy — one
// helper, one behaviour.
