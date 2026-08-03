package contract

import (
	"os"
	"slices"
	"strings"
	"testing"
)

const adminAttendancePath = "/api/v1/admin/events/{event_id}/attendance"

func requireTeacherAdminAttendanceATDD(t *testing.T) {
	t.Helper()
	if os.Getenv("ATDD_ACTIVE_09") != "1" {
		t.Skip("red-phase scaffold for 0.9; set ATDD_ACTIVE_09=1 while implementing")
	}
}

func TestTeacherAdminAttendanceContract_AdminAttendanceRead(t *testing.T) {
	requireTeacherAdminAttendanceATDD(t)

	doc := loadSpec(t, repoRoot(t))
	paths := getMap(doc, "paths")
	op := getMap(getMap(paths, adminAttendancePath), "get")
	if op == nil {
		t.Fatalf("[P0] missing admin attendance path GET %s", adminAttendancePath)
	}
	if got := getStr(op, "operationId"); got != "listAdminEventAttendance" {
		t.Errorf("[P0] operationId = %q, want listAdminEventAttendance", got)
	}
	assertBearerSecurity(t, op, "GET "+adminAttendancePath)
	if ref := getStr(jsonResponseSchema(doc, op, "200"), "$ref"); ref != "#/components/schemas/AttendanceRecordCollection" {
		t.Errorf("[P0] 200 schema = %q, want AttendanceRecordCollection", ref)
	}
	assertResponseFamily(t, doc, op, map[string]string{
		"400": "#/components/schemas/ValidationError",
		"401": "#/components/schemas/Error",
		"403": "#/components/schemas/Error",
		"404": "#/components/schemas/Error",
		"500": "#/components/schemas/Error",
	})

	params, _ := op["parameters"].([]any)
	found := map[string]map[string]any{}
	for _, raw := range params {
		p := asMap(raw)
		found[getStr(p, "name")] = p
	}
	for _, name := range []string{"event_id", "category_id", "slot", "page", "page_size"} {
		if found[name] == nil {
			t.Errorf("[P0] GET %s is missing parameter %s", adminAttendancePath, name)
		}
	}
	if slot := found["slot"]; slot != nil {
		schema := resolveSchema(doc, getMap(slot, "schema"))
		if got := getFloat(schema, "minimum"); got != 1 {
			t.Errorf("[P0] slot.minimum = %v, want 1", got)
		}
		if got := getFloat(schema, "maximum"); got != 12 {
			t.Errorf("[P0] slot.maximum = %v, want 12", got)
		}
	}
}

func TestTeacherAdminAttendanceContract_AttendanceSchemas(t *testing.T) {
	requireTeacherAdminAttendanceATDD(t)

	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	for _, name := range []string{"AttendanceRecord", "AttendanceActor", "AttendanceRecordCollection"} {
		if getMap(schemas, name) == nil {
			t.Errorf("[P0] components.schemas.%s is missing", name)
		}
	}

	record := getMap(schemas, "AttendanceRecord")
	if record != nil {
		if missing := missingRequired(record,
			"record_id", "student", "event_id", "category", "slot", "capture_method", "marked_by", "actor_role", "server_received_at",
		); len(missing) > 0 {
			t.Errorf("[P0] AttendanceRecord required is missing %v", missing)
		}
		props := getMap(record, "properties")
		for _, forbidden := range []string{"card", "token", "token_fingerprint", "fallback_credit", "dispute", "reason"} {
			if _, ok := props[forbidden]; ok {
				t.Errorf("[P0] AttendanceRecord must not expose deferred/private field %s", forbidden)
			}
		}
		if got := enumStrings(resolveSchema(doc, getMap(props, "capture_method"))); !slices.Contains(got, "search") {
			t.Errorf("[P0] AttendanceRecord.capture_method enum = %v, want search included", got)
		}
	}

	actor := getMap(schemas, "AttendanceActor")
	if actor != nil {
		if missing := missingRequired(actor, "user_id", "email", "name"); len(missing) > 0 {
			t.Errorf("[P1] AttendanceActor required is missing %v; nullable keys must stay present after actor deletion", missing)
		}
		props := getMap(actor, "properties")
		for _, field := range []string{"user_id", "email", "name"} {
			if nullable, _ := getMap(props, field)["nullable"].(bool); !nullable {
				t.Errorf("[P1] AttendanceActor.%s must be nullable for ON DELETE SET NULL actor rows", field)
			}
		}
	}

	collection := getMap(schemas, "AttendanceRecordCollection")
	if collection != nil {
		if missing := missingRequired(collection, "data", "page", "page_size", "total"); len(missing) > 0 {
			t.Errorf("[P0] AttendanceRecordCollection required is missing %v", missing)
		}
	}
}

func TestTeacherAdminAttendanceContract_NoDeferredManualManagementSurface(t *testing.T) {
	requireTeacherAdminAttendanceATDD(t)

	doc := loadSpec(t, repoRoot(t))
	paths := getMap(doc, "paths")
	for path, raw := range paths {
		if path == adminAttendancePath {
			continue
		}
		item := asMap(raw)
		for method := range item {
			id := getStr(getMap(item, method), "operationId")
			for _, forbidden := range []string{"Correction", "Dispute", "Export", "ManualOverride", "DeleteAttendance"} {
				if strings.Contains(id, forbidden) || strings.Contains(path, strings.ToLower(forbidden)) {
					t.Errorf("[P1] 0.9 must not introduce deferred %s surface at %s %s", forbidden, method, path)
				}
			}
		}
	}
}
