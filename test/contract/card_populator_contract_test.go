//go:build atdd

package contract

import "testing"

const adminStudentListPath = "/api/v1/admin/students"

func TestCardPopulatorContract_AdminStudentListOperation(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	path := getMap(getMap(doc, "paths"), adminStudentListPath)
	if path == nil {
		t.Fatalf("[P0] missing admin student list path %s", adminStudentListPath)
	}
	get := getMap(path, "get")
	if get == nil {
		t.Fatalf("[P0] missing GET %s", adminStudentListPath)
	}
	if got := getStr(get, "operationId"); got != "listAdminStudents" {
		t.Errorf("[P0] operationId = %q, want listAdminStudents", got)
	}
	assertBearerSecurity(t, get, "GET "+adminStudentListPath)
	if ref := getStr(jsonResponseSchema(doc, get, "200"), "$ref"); ref != "#/components/schemas/StudentCollection" {
		t.Errorf("[P0] 200 schema = %q, want StudentCollection", ref)
	}
	assertQueryParameter(t, get, "branch_id")
	assertQueryParameter(t, get, "batch")
	assertQueryParameter(t, get, "section")
	assertQueryParameter(t, get, "page")
	assertQueryParameter(t, get, "page_size")
	assertResponseFamily(t, doc, get, map[string]string{
		"400": "#/components/schemas/ValidationError",
		"401": "#/components/schemas/Error",
		"403": "#/components/schemas/Error",
		"500": "#/components/schemas/Error",
	})
}

func TestCardPopulatorContract_StudentListShapeIsBoundedAndTokenFree(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")
	collection := getMap(schemas, "StudentCollection")
	if collection == nil {
		t.Fatal("[P0] components.schemas.StudentCollection is missing")
	}
	props := getMap(collection, "properties")
	for _, field := range []string{"data", "page", "page_size", "total"} {
		if _, ok := props[field]; !ok {
			t.Errorf("[P0] StudentCollection missing %s", field)
		}
	}
	if missing := missingRequired(collection, "data", "page", "page_size", "total"); len(missing) > 0 {
		t.Errorf("[P0] StudentCollection required is missing %v", missing)
	}
	item := resolveSchema(doc, getMap(getMap(props, "data"), "items"))
	itemProps := getMap(item, "properties")
	for _, field := range []string{"student_id", "roll_no", "display_name", "branch", "section", "batch"} {
		if _, ok := itemProps[field]; !ok {
			t.Errorf("[P0] StudentCollection item missing %s", field)
		}
	}
	for _, forbidden := range []string{"email", "token", "payload", "hmac_key", "key"} {
		if _, ok := itemProps[forbidden]; ok {
			t.Errorf("[P0] StudentCollection item must not expose %s", forbidden)
		}
	}
}

func TestCardPopulatorContract_ReusesServerSideSealAndResolveShapes(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	for _, path := range []string{cardSealPath, cardResolvePath} {
		if getMap(getMap(doc, "paths"), path) == nil {
			t.Fatalf("[P0] required card-token operation missing: %s", path)
		}
	}
	payloadProps := schemaProps(doc, getMap(getMap(getMap(getMap(doc, "components"), "schemas"), "CardSeal"), "properties")["payload"].(map[string]any))
	for _, field := range []string{"version", "token"} {
		if _, ok := payloadProps[field]; !ok {
			t.Errorf("[P0] CardSeal.payload missing %s", field)
		}
	}
	for _, forbidden := range []string{"roll_no", "display_name", "student_id", "branch", "section", "batch", "email", "key"} {
		if _, ok := payloadProps[forbidden]; ok {
			t.Errorf("[P0] CardSeal.payload must not expose %s", forbidden)
		}
	}
}

func assertQueryParameter(t *testing.T, op map[string]any, name string) {
	t.Helper()
	raw, ok := op["parameters"].([]any)
	if !ok {
		t.Fatalf("[P0] operation has no query parameters")
	}
	for _, candidate := range raw {
		param := asMap(candidate)
		if getStr(param, "name") == name && getStr(param, "in") == "query" {
			return
		}
	}
	t.Errorf("[P0] missing query parameter %q", name)
}
