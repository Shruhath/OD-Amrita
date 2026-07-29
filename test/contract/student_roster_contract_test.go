package contract

import "testing"

const studentImportPath = "/api/v1/admin/students/import"

func TestStudentRosterContract_MultipartImportOperation(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	item := getMap(getMap(doc, "paths"), studentImportPath)
	if item == nil {
		t.Fatalf("[P0] missing student import path %s", studentImportPath)
	}
	post := getMap(item, "post")
	if post == nil {
		t.Fatalf("[P0] missing POST %s", studentImportPath)
	}
	if got := getStr(post, "operationId"); got != "importAdminStudents" {
		t.Errorf("[P1] operationId = %q, want importAdminStudents", got)
	}
	security, ok := post["security"].([]any)
	if !ok || len(security) == 0 {
		t.Fatal("[P0] student import must require bearer authentication")
	}
	body := getMap(post, "requestBody")
	content := getMap(body, "content")
	multipart := getMap(content, "multipart/form-data")
	if multipart == nil {
		t.Fatal("[P0] import request must use multipart/form-data")
	}
	schema := getMap(multipart, "schema")
	props := getMap(schema, "properties")
	file := getMap(props, "file")
	if file == nil {
		t.Fatal("[P0] multipart request must declare file field")
	}
	if got := getStr(file, "format"); got != "binary" {
		t.Errorf("[P1] file format = %q, want binary", got)
	}
}

func TestStudentRosterContract_ResultAndRowErrorSchemas(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")
	result := getMap(schemas, "StudentRosterImportResult")
	if result == nil {
		t.Fatal("[P0] StudentRosterImportResult schema is missing")
	}
	props := getMap(result, "properties")
	for _, field := range []string{"import_id", "total_rows", "inserted_rows", "updated_rows", "unchanged_rows", "invalid_rows", "database_count", "errors"} {
		if _, ok := props[field]; !ok {
			t.Errorf("[P0] StudentRosterImportResult missing %s", field)
		}
	}
	if got := getStr(getMap(props, "import_id"), "format"); got != "uuid" {
		t.Errorf("[P1] import_id format = %q, want uuid", got)
	}
	errors := getMap(props, "errors")
	if got := getStr(getMap(errors, "items"), "$ref"); got != "#/components/schemas/StudentRosterImportError" {
		t.Errorf("[P1] errors item ref = %q, want StudentRosterImportError", got)
	}

	rowError := getMap(getMap(schemas, "StudentRosterImportError"), "properties")
	for _, field := range []string{"row", "field", "code", "message"} {
		if _, ok := rowError[field]; !ok {
			t.Errorf("[P0] StudentRosterImportError missing %s", field)
		}
	}
}

func TestStudentRosterContract_ResponseFamily(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	post := getMap(getMap(getMap(doc, "paths"), studentImportPath), "post")
	for _, status := range []string{"201", "400", "401", "403", "500"} {
		if jsonResponseSchema(doc, post, status) == nil {
			t.Errorf("[P0] POST %s must define %s response", studentImportPath, status)
		}
	}
	if ref := getStr(jsonResponseSchema(doc, post, "201"), "$ref"); ref != "#/components/schemas/StudentRosterImportResult" {
		t.Errorf("[P1] 201 schema = %q, want StudentRosterImportResult", ref)
	}
	if ref := getStr(jsonResponseSchema(doc, post, "400"), "$ref"); ref != "#/components/schemas/ValidationError" {
		t.Errorf("[P1] 400 schema = %q, want ValidationError", ref)
	}
}
