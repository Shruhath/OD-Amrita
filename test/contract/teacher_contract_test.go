package contract

import (
	"slices"
	"testing"
)

// Acceptance coverage for teacher profile creation and branch
// association, AC 1-6.
//
// These read contracts/openapi.yaml directly, so they assert the SSOT itself
// rather than the generated output derived from it. This file only calls the
// shared contract-test helpers (loadSpec/getMap/getStr/enumStrings/
// jsonResponseSchema) already in this package.
//
// Invoked by: api-ci -> acceptance job (`go test ./... -v` in test/contract).

const teachersPath = "/api/v1/admin/teachers"

func TestTeacherContract_OperationsRequireBearerAuth(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	item := getMap(getMap(doc, "paths"), teachersPath)
	if item == nil {
		t.Fatalf("[P0] missing teacher path %s", teachersPath)
	}

	for _, method := range []string{"get", "post"} {
		op := getMap(item, method)
		if op == nil {
			t.Errorf("[P0] missing %s %s", method, teachersPath)
			continue
		}
		security, ok := op["security"].([]any)
		if !ok || len(security) == 0 {
			t.Errorf("[P0] %s %s must require bearer authentication", method, teachersPath)
		}
	}

	if got := getStr(getMap(item, "get"), "operationId"); got != "listAdminTeachers" {
		t.Errorf("[P1] list operationId = %q, want listAdminTeachers", got)
	}
	if got := getStr(getMap(item, "post"), "operationId"); got != "createAdminTeacherProfile" {
		t.Errorf("[P1] create operationId = %q, want createAdminTeacherProfile", got)
	}
}

func TestTeacherContract_ExposesNoItemRouteOrMutationBeyondCreate(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	paths := getMap(doc, "paths")

	// D3: adding branches to an existing teacher, and role reversal, are both
	// explicitly out of scope. No per-teacher path may exist.
	item := getMap(paths, teachersPath)
	for _, method := range []string{"delete", "put", "patch"} {
		if getMap(item, method) != nil {
			t.Errorf("[P0] %s %s must not exist", method, teachersPath)
		}
	}
	if getMap(paths, teachersPath+"/{user_id}") != nil {
		t.Error("[P0] no per-teacher item path may exist: teacher profiles are create + list only")
	}
}

func TestTeacherContract_SchemasAreGeneratedTypeBacked(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	for _, name := range []string{"TeacherProfile", "TeacherProfileCollection", "CreateTeacherProfileRequest"} {
		if getMap(schemas, name) == nil {
			t.Errorf("[P0] components.schemas.%s is missing", name)
		}
	}

	profile := getMap(getMap(schemas, "TeacherProfile"), "properties")
	for _, field := range []string{"user_id", "email", "name", "branches", "created_at"} {
		if _, ok := profile[field]; !ok {
			t.Errorf("[P0] TeacherProfile missing %s", field)
		}
	}
	if got := getStr(getMap(profile, "user_id"), "format"); got != "uuid" {
		t.Errorf("[P1] TeacherProfile.user_id format = %q, want uuid", got)
	}
	if got := getStr(getMap(profile, "created_at"), "format"); got != "date-time" {
		t.Errorf("[P1] TeacherProfile.created_at format = %q, want date-time", got)
	}
	// name is nullable — some providers omit the display name, the same
	// AuthenticatedIdentity.name precedent reused here.
	branchesItems := getMap(getMap(profile, "branches"), "items")
	if got := getStr(branchesItems, "$ref"); got != "#/components/schemas/BranchSummary" {
		t.Errorf("[P1] TeacherProfile.branches must reuse BranchSummary, got ref %q", got)
	}

	required, _ := getMap(schemas, "TeacherProfile")["required"].([]any)
	requiredSet := make(map[string]bool, len(required))
	for _, r := range required {
		if s, ok := r.(string); ok {
			requiredSet[s] = true
		}
	}
	if requiredSet["name"] {
		t.Error("[P1] TeacherProfile.name must be the only optional field (nullable name, some providers omit it)")
	}
	for _, field := range []string{"user_id", "email", "branches", "created_at"} {
		if !requiredSet[field] {
			t.Errorf("[P1] TeacherProfile.%s must be required", field)
		}
	}

	create := getMap(getMap(schemas, "CreateTeacherProfileRequest"), "properties")
	email := getMap(create, "email")
	if email == nil {
		t.Fatal("[P0] CreateTeacherProfileRequest.email is missing")
	}
	if got := jsonNumber(email, "minLength"); got != 1 {
		t.Errorf("[P1] CreateTeacherProfileRequest.email minLength = %v, want 1", got)
	}
	if got := jsonNumber(email, "maxLength"); got != 320 {
		t.Errorf("[P1] CreateTeacherProfileRequest.email maxLength = %v, want 320", got)
	}
	branchIDs := getMap(create, "branch_ids")
	if branchIDs == nil {
		t.Fatal("[P0] CreateTeacherProfileRequest.branch_ids is missing")
	}
	if got := jsonNumber(branchIDs, "minItems"); got != 1 {
		t.Errorf("[P1] CreateTeacherProfileRequest.branch_ids minItems = %v, want 1", got)
	}
	if got := jsonNumber(branchIDs, "maxItems"); got != 100 {
		t.Errorf("[P1] CreateTeacherProfileRequest.branch_ids maxItems = %v, want 100", got)
	}
	if unique, _ := branchIDs["uniqueItems"].(bool); !unique {
		t.Error("[P1] CreateTeacherProfileRequest.branch_ids must declare uniqueItems: true")
	}
}

func TestTeacherContract_CollectionIsPaginatedLikeEventsNotBoundedLikeBranches(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")
	collection := getMap(getMap(schemas, "TeacherProfileCollection"), "properties")
	if collection == nil {
		t.Fatal("[P0] TeacherProfileCollection is missing")
	}

	// D4: the teacher roster scales with headcount, so it follows
	// EventCollection's page/page_size/total shape, unlike CategoryCollection.
	for _, field := range []string{"data", "page", "page_size", "total"} {
		if _, ok := collection[field]; !ok {
			t.Errorf("[P0] TeacherProfileCollection must declare %s", field)
		}
	}

	list := getMap(getMap(doc, "paths"), teachersPath)["get"]
	params, _ := asMap(list)["parameters"].([]any)
	names := make(map[string]bool, len(params))
	for _, p := range params {
		if pm, ok := p.(map[string]any); ok {
			names[getStr(pm, "name")] = true
		}
	}
	if !names["page"] || !names["page_size"] {
		t.Error("[P0] GET " + teachersPath + " must accept page and page_size query parameters")
	}
	// No status filter: teachers have no archive lifecycle, unlike events.
	if names["status"] {
		t.Error("[P1] GET " + teachersPath + " must not declare a status filter — teachers have no archive lifecycle")
	}
}

func TestTeacherContract_ResponseFamilyCoversEveryRejection(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	item := getMap(getMap(doc, "paths"), teachersPath)

	list := getMap(item, "get")
	for _, status := range []string{"200", "400", "401", "403", "500"} {
		if jsonResponseSchema(doc, list, status) == nil {
			t.Errorf("[P1] GET %s must define a %s response", teachersPath, status)
		}
	}

	create := getMap(item, "post")
	// 404 (USER_NOT_FOUND) and 409 (ROLE_ALREADY_ASSIGNED) are what make AC 2
	// observable on the wire.
	for _, status := range []string{"201", "400", "401", "403", "404", "409", "500"} {
		if jsonResponseSchema(doc, create, status) == nil {
			t.Errorf("[P0] POST %s must define a %s response", teachersPath, status)
		}
	}
	if ref := getStr(jsonResponseSchema(doc, create, "400"), "$ref"); ref != "#/components/schemas/ValidationError" {
		t.Errorf("[P1] POST %s 400 must use ValidationError, got %q", teachersPath, ref)
	}
	if ref := getStr(jsonResponseSchema(doc, create, "201"), "$ref"); ref != "#/components/schemas/TeacherProfile" {
		t.Errorf("[P1] POST %s 201 must return TeacherProfile, got %q", teachersPath, ref)
	}
}

func TestTeacherContract_NewErrorCodesJoinTheEnum(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")
	code := getMap(getMap(getMap(schemas, "Error"), "properties"), "code")
	if code == nil {
		t.Fatal("[P0] Error.code is missing")
	}

	codes := enumStrings(code)
	// The generated enum is the wire SSOT: api/internal/domain/errors.go maps
	// each to an HTTP status and errors_test.go's parity test fails on any
	// contract code without a mapping, so the contract and Go must move
	// together (see errorCodeRegistry in this package for the existing set).
	for _, want := range []string{"USER_NOT_FOUND", "ROLE_ALREADY_ASSIGNED"} {
		if !slices.Contains(codes, want) {
			t.Errorf("[P0] Error.code enum must contain %s, got %v", want, codes)
		}
	}
}

func TestTeacherContract_EventAndCategorySchemasStayUnchanged(t *testing.T) {
	// Teacher profiles add a new aggregate; they must not reshape the event and
	// category wire contracts. A teacher profile is not part of the event aggregate.
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	event := getMap(schemas, "Event")
	if _, ok := getMap(event, "properties")["teachers"]; ok {
		t.Error("[P2] Event must not embed teachers")
	}
	category := getMap(schemas, "Category")
	if _, ok := getMap(category, "properties")["teachers"]; ok {
		t.Error("[P2] Category must not embed teachers")
	}
}
