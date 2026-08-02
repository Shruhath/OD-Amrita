//go:build atdd

package contract

import "testing"

const markPath = "/api/v1/mark"

func TestODMarkContract_SynchronousProtectedOperation(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	path := getMap(getMap(doc, "paths"), markPath)
	if path == nil {
		t.Fatalf("[P0] missing synchronous mark path %s", markPath)
	}
	post := getMap(path, "post")
	if post == nil {
		t.Fatalf("[P0] missing POST %s", markPath)
	}
	if got := getStr(post, "operationId"); got != "markAttendance" {
		t.Errorf("[P0] operationId = %q, want markAttendance", got)
	}
	assertBearerSecurity(t, post, "POST "+markPath)
	if ref := getStr(jsonRequestSchema(post), "$ref"); ref != "#/components/schemas/MarkAttendanceRequest" {
		t.Errorf("[P0] request schema = %q, want MarkAttendanceRequest", ref)
	}
	if ref := getStr(jsonResponseSchema(doc, post, "201"), "$ref"); ref != "#/components/schemas/MarkAttendanceResult" {
		t.Errorf("[P0] 201 schema = %q, want MarkAttendanceResult", ref)
	}
	assertResponseFamily(t, doc, post, map[string]string{
		"400": "#/components/schemas/ValidationError",
		"401": "#/components/schemas/Error",
		"403": "#/components/schemas/Error",
		"404": "#/components/schemas/Error",
		"409": "#/components/schemas/Error",
		"422": "#/components/schemas/Error",
		"500": "#/components/schemas/Error",
	})
}

func TestODMarkContract_RequestShapePinsIdentityAndCaptureSemantics(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")
	request := getMap(schemas, "MarkAttendanceRequest")
	if request == nil {
		t.Fatal("[P0] components.schemas.MarkAttendanceRequest is missing")
	}
	if missing := missingRequired(request, "event_id", "category_id", "capture_method"); len(missing) > 0 {
		t.Errorf("[P0] MarkAttendanceRequest required is missing %v", missing)
	}
	if oneOf, ok := request["oneOf"].([]any); !ok || len(oneOf) != 2 {
		t.Fatalf("[P1] MarkAttendanceRequest must document exactly one student_id/card identity source")
	}
	props := getMap(request, "properties")
	for _, field := range []string{"event_id", "category_id", "capture_method", "student_id", "card", "slot"} {
		if _, ok := props[field]; !ok {
			t.Errorf("[P0] MarkAttendanceRequest missing %s", field)
		}
	}
	if ref := getStr(getMap(props, "card"), "$ref"); ref != "#/components/schemas/CardTokenResolveRequest" {
		t.Errorf("[P0] card schema = %q, want CardTokenResolveRequest", ref)
	}
	method := getMap(props, "capture_method")
	if missing := containsAll(enumStrings(method), []string{"nfc", "camera", "search"}); len(missing) > 0 {
		t.Errorf("[P0] capture_method enum missing %v", missing)
	}
	slot := getMap(props, "slot")
	if got := getFloat(slot, "minimum"); got != 1 {
		t.Errorf("[P0] slot.minimum = %v, want 1", got)
	}
	if got := getFloat(slot, "maximum"); got != 12 {
		t.Errorf("[P0] slot.maximum = %v, want 12", got)
	}
	for _, forbidden := range []string{"roll_no", "client_timestamp", "client_slot", "scan_id"} {
		if _, ok := props[forbidden]; ok {
			t.Errorf("[P0] MarkAttendanceRequest must not expose %s", forbidden)
		}
	}
}

func TestODMarkContract_ResponseShapeAndErrors(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")
	result := getMap(schemas, "MarkAttendanceResult")
	if result == nil {
		t.Fatal("[P0] components.schemas.MarkAttendanceResult is missing")
	}
	resultProps := getMap(result, "properties")
	for _, field := range []string{"record_id", "student", "event_id", "category_id", "slot", "capture_method", "server_received_at"} {
		if _, ok := resultProps[field]; !ok {
			t.Errorf("[P0] MarkAttendanceResult missing %s", field)
		}
	}
	if missing := missingRequired(result, "record_id", "student", "event_id", "category_id", "slot", "capture_method", "server_received_at"); len(missing) > 0 {
		t.Errorf("[P0] MarkAttendanceResult required is missing %v", missing)
	}

	student := getMap(schemas, "MarkedStudent")
	if student == nil {
		t.Fatal("[P0] components.schemas.MarkedStudent is missing")
	}
	studentProps := getMap(student, "properties")
	for _, field := range []string{"student_id", "roll_no", "display_name", "branch", "section", "batch"} {
		if _, ok := studentProps[field]; !ok {
			t.Errorf("[P0] MarkedStudent missing %s", field)
		}
	}
	for _, forbidden := range []string{"token", "hmac_key", "key"} {
		if _, ok := studentProps[forbidden]; ok {
			t.Errorf("[P0] MarkedStudent must not expose %s", forbidden)
		}
	}

	errorSchema := getMap(schemas, "Error")
	code := getMap(getMap(errorSchema, "properties"), "code")
	missing := containsAll(enumStrings(code), []string{
		"SCAN_DUPLICATE",
		"SLOT_CLOSED",
		"CARD_NOT_RECOGNIZED",
		"EVENT_NOT_FOUND",
		"EVENT_ARCHIVED",
		"CATEGORY_NOT_FOUND",
		"STUDENT_NOT_FOUND",
		"RBAC_FORBIDDEN",
		"VALIDATION_FAILED",
	})
	if len(missing) > 0 {
		t.Errorf("[P0] Error.code enum missing %v", missing)
	}
}

// getFloat now lives in contract_acceptance_test.go, which carries no build tag,
// so the untagged marking-surface gate can use it too.
