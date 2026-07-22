package contract

import (
	"slices"
	"testing"
)

func TestEventContract_AdminOperationsAndNoDelete(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	paths := getMap(doc, "paths")

	want := map[string][]string{
		"/api/v1/admin/branches":                  {"get"},
		"/api/v1/admin/events":                    {"get", "post"},
		"/api/v1/admin/events/{event_id}":         {"get"},
		"/api/v1/admin/events/{event_id}/archive": {"post"},
		// Categories are part of the same Admin-only family, so they belong in
		// the one table that asserts bearer auth across every admin operation.
		"/api/v1/admin/events/{event_id}/categories": {"get", "post"},
	}
	for path, methods := range want {
		item := getMap(paths, path)
		if item == nil {
			t.Errorf("missing Admin event path %s", path)
			continue
		}
		for _, method := range methods {
			op := getMap(item, method)
			if op == nil {
				t.Errorf("missing %s %s", method, path)
				continue
			}
			security, ok := op["security"].([]any)
			if !ok || len(security) == 0 {
				t.Errorf("%s %s must require bearer authentication", method, path)
			}
		}
	}

	if item := getMap(paths, "/api/v1/admin/events/{event_id}"); getMap(item, "delete") != nil {
		t.Error("DELETE /api/v1/admin/events/{event_id} must not exist")
	}
}

func TestEventContract_SchemasAreGeneratedTypeBacked(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	for _, name := range []string{"BranchSummary", "Event", "EventStatus", "CreateEventRequest", "EventCollection"} {
		if getMap(schemas, name) == nil {
			t.Errorf("components.schemas.%s is missing", name)
		}
	}

	branchProps := getMap(getMap(schemas, "BranchSummary"), "properties")
	for _, field := range []string{"id", "code", "name"} {
		if _, ok := branchProps[field]; !ok {
			t.Errorf("BranchSummary missing %s", field)
		}
	}

	eventProps := getMap(getMap(schemas, "Event"), "properties")
	for _, field := range []string{"id", "name", "event_date", "status", "branches", "created_at", "archived_at"} {
		if _, ok := eventProps[field]; !ok {
			t.Errorf("Event missing %s", field)
		}
	}
	if got := getStr(getMap(eventProps, "event_date"), "format"); got != "date" {
		t.Errorf("Event.event_date format = %q, want date", got)
	}
	if got := enumStrings(getMap(schemas, "EventStatus")); !slices.Equal(got, []string{"active", "archived"}) {
		t.Errorf("EventStatus = %v, want [active archived]", got)
	}

	createProps := getMap(getMap(schemas, "CreateEventRequest"), "properties")
	for _, field := range []string{"name", "event_date", "branch_ids"} {
		if _, ok := createProps[field]; !ok {
			t.Errorf("CreateEventRequest missing %s", field)
		}
	}
	branchIDs := getMap(createProps, "branch_ids")
	unique, _ := branchIDs["uniqueItems"].(bool)
	if getStr(branchIDs, "type") != "array" || !unique {
		t.Error("CreateEventRequest.branch_ids must be an array with unique items")
	}
}

func TestEventContract_ListSemanticsAndValidationResponses(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	paths := getMap(doc, "paths")
	list := getMap(getMap(paths, "/api/v1/admin/events"), "get")
	if list == nil {
		t.Fatal("GET /api/v1/admin/events is missing")
	}

	params, ok := list["parameters"].([]any)
	if !ok {
		t.Fatal("GET /api/v1/admin/events parameters are missing")
	}
	seen := map[string]map[string]any{}
	for _, raw := range params {
		param := asMap(raw)
		seen[getStr(param, "name")] = getMap(param, "schema")
	}
	for _, name := range []string{"status", "page", "page_size"} {
		if seen[name] == nil {
			t.Errorf("GET events missing %s query parameter", name)
		}
	}
	if missing := containsAll(enumStrings(seen["status"]), []string{"active", "archived", "all"}); len(missing) > 0 {
		t.Errorf("status query enum missing %v", missing)
	}
	if getStr(seen["status"], "default") != "all" {
		t.Error("status query must default to all")
	}

	for _, method := range []string{"get", "post"} {
		op := getMap(getMap(paths, "/api/v1/admin/events"), method)
		if ref := getStr(jsonResponseSchema(doc, op, "400"), "$ref"); ref != "#/components/schemas/ValidationError" {
			t.Errorf("%s /api/v1/admin/events 400 must use ValidationError", method)
		}
	}

	archive := getMap(getMap(paths, "/api/v1/admin/events/{event_id}/archive"), "post")
	if jsonResponseSchema(doc, archive, "404") == nil {
		t.Error("archive must define a 404 response")
	}
}
