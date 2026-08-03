package contract

import (
	"slices"
	"testing"
)

const coordinatorAssignmentsPath = "/api/v1/admin/events/{event_id}/coordinators"

func TestCoordinatorAssignmentContract_NestedOperationsRequireBearerAuth(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	item := getMap(getMap(doc, "paths"), coordinatorAssignmentsPath)
	if item == nil {
		t.Fatalf("[P0] missing coordinator assignment path %s", coordinatorAssignmentsPath)
	}

	for _, method := range []string{"get", "post"} {
		op := getMap(item, method)
		if op == nil {
			t.Errorf("[P0] missing %s %s", method, coordinatorAssignmentsPath)
			continue
		}
		security, ok := op["security"].([]any)
		if !ok || len(security) == 0 {
			t.Errorf("[P0] %s %s must require bearer authentication", method, coordinatorAssignmentsPath)
		}
	}

	if got := getStr(getMap(item, "get"), "operationId"); got != "listAdminEventCoordinators" {
		t.Errorf("[P1] list operationId = %q, want listAdminEventCoordinators", got)
	}
	if got := getStr(getMap(item, "post"), "operationId"); got != "createAdminEventCoordinatorAssignment" {
		t.Errorf("[P1] create operationId = %q, want createAdminEventCoordinatorAssignment", got)
	}
}

func TestCoordinatorAssignmentContract_ExposesOnlyListAndCreate(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	paths := getMap(doc, "paths")
	item := getMap(paths, coordinatorAssignmentsPath)
	if item == nil {
		t.Fatalf("[P0] missing coordinator assignment path %s", coordinatorAssignmentsPath)
	}

	for _, method := range []string{"delete", "put", "patch"} {
		if getMap(item, method) != nil {
			t.Errorf("[P0] %s %s must not exist", method, coordinatorAssignmentsPath)
		}
	}
	if getMap(paths, coordinatorAssignmentsPath+"/{assignment_id}") != nil {
		t.Error("[P0] no per-assignment item path may exist: this release has no removal or update operation")
	}
}

func TestCoordinatorAssignmentContract_SchemasAreGeneratedTypeBacked(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	for _, name := range []string{"CoordinatorAssignment", "CoordinatorAssignmentCollection", "CreateCoordinatorAssignmentRequest"} {
		if getMap(schemas, name) == nil {
			t.Errorf("[P0] components.schemas.%s is missing", name)
		}
	}

	assignment := getMap(schemas, "CoordinatorAssignment")
	props := getMap(assignment, "properties")
	for _, field := range []string{"id", "user_id", "email", "name", "event_id", "created_at"} {
		if _, ok := props[field]; !ok {
			t.Errorf("[P0] CoordinatorAssignment missing %s", field)
		}
	}
	if _, ok := props["category_id"]; ok {
		t.Error("[P0] CoordinatorAssignment must not expose category_id; assignment is event-level only")
	}
	if _, ok := props["scope"]; ok {
		t.Error("[P0] CoordinatorAssignment must not expose scope; category/overall assignment is out of scope")
	}
	for _, field := range []string{"id", "user_id", "event_id"} {
		if got := getStr(getMap(props, field), "format"); got != "uuid" {
			t.Errorf("[P1] CoordinatorAssignment.%s format = %q, want uuid", field, got)
		}
	}
	if got := getStr(getMap(props, "created_at"), "format"); got != "date-time" {
		t.Errorf("[P1] CoordinatorAssignment.created_at format = %q, want date-time", got)
	}
	if nullable, _ := getMap(props, "name")["nullable"].(bool); !nullable {
		t.Error("[P1] CoordinatorAssignment.name must be nullable because provider display names may be absent")
	}

	required, _ := assignment["required"].([]any)
	requiredSet := map[string]bool{}
	for _, field := range required {
		if value, ok := field.(string); ok {
			requiredSet[value] = true
		}
	}
	for _, field := range []string{"id", "user_id", "email", "name", "event_id", "created_at"} {
		if !requiredSet[field] {
			t.Errorf("[P1] CoordinatorAssignment.%s must be required", field)
		}
	}

	create := getMap(getMap(schemas, "CreateCoordinatorAssignmentRequest"), "properties")
	email := getMap(create, "email")
	if email == nil {
		t.Fatal("[P0] CreateCoordinatorAssignmentRequest.email is missing")
	}
	if got := jsonNumber(email, "minLength"); got != 1 {
		t.Errorf("[P1] CreateCoordinatorAssignmentRequest.email minLength = %v, want 1", got)
	}
	if got := jsonNumber(email, "maxLength"); got != 320 {
		t.Errorf("[P1] CreateCoordinatorAssignmentRequest.email maxLength = %v, want 320", got)
	}
	if _, ok := create["category_id"]; ok {
		t.Error("[P0] CreateCoordinatorAssignmentRequest must not accept category_id")
	}
}

func TestCoordinatorAssignmentContract_CollectionIsBoundedReferenceDataNotPaginated(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")
	collection := getMap(getMap(schemas, "CoordinatorAssignmentCollection"), "properties")
	if collection == nil {
		t.Fatal("[P0] CoordinatorAssignmentCollection is missing")
	}
	if _, ok := collection["data"]; !ok {
		t.Error("[P0] CoordinatorAssignmentCollection must wrap rows in data")
	}
	for _, forbidden := range []string{"page", "page_size", "total"} {
		if _, ok := collection[forbidden]; ok {
			t.Errorf("[P1] CoordinatorAssignmentCollection must not declare %s", forbidden)
		}
	}
}

func TestCoordinatorAssignmentContract_ResponseFamilyCoversEveryRejection(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	item := getMap(getMap(doc, "paths"), coordinatorAssignmentsPath)

	list := getMap(item, "get")
	for _, status := range []string{"200", "400", "401", "403", "404", "500"} {
		if jsonResponseSchema(doc, list, status) == nil {
			t.Errorf("[P1] GET %s must define a %s response", coordinatorAssignmentsPath, status)
		}
	}

	create := getMap(item, "post")
	for _, status := range []string{"201", "400", "401", "403", "404", "409", "500"} {
		if jsonResponseSchema(doc, create, status) == nil {
			t.Errorf("[P0] POST %s must define a %s response", coordinatorAssignmentsPath, status)
		}
	}
	if ref := getStr(jsonResponseSchema(doc, create, "400"), "$ref"); ref != "#/components/schemas/ValidationError" {
		t.Errorf("[P1] POST %s 400 must use ValidationError, got %q", coordinatorAssignmentsPath, ref)
	}
	if ref := getStr(jsonResponseSchema(doc, create, "201"), "$ref"); ref != "#/components/schemas/CoordinatorAssignment" {
		t.Errorf("[P1] POST %s 201 must return CoordinatorAssignment, got %q", coordinatorAssignmentsPath, ref)
	}
}

func TestCoordinatorAssignmentContract_NewDuplicateErrorJoinsTheEnum(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")
	code := getMap(getMap(getMap(schemas, "Error"), "properties"), "code")
	if code == nil {
		t.Fatal("[P0] Error.code is missing")
	}

	codes := enumStrings(code)
	if !slices.Contains(codes, "COORDINATOR_ASSIGNMENT_EXISTS") {
		t.Errorf("[P0] Error.code enum must contain COORDINATOR_ASSIGNMENT_EXISTS, got %v", codes)
	}
	for _, reused := range []string{"USER_NOT_FOUND", "VALIDATION_FAILED", "EVENT_NOT_FOUND", "EVENT_ARCHIVED", "ROLE_ALREADY_ASSIGNED"} {
		if !slices.Contains(codes, reused) {
			t.Errorf("[P0] Error.code enum must retain reused code %s", reused)
		}
	}
}

func TestCoordinatorAssignmentContract_EventAndCategorySchemasStayUnchanged(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	if _, ok := getMap(getMap(schemas, "Event"), "properties")["coordinators"]; ok {
		t.Error("[P2] Event must not embed coordinator assignments")
	}
	if _, ok := getMap(getMap(schemas, "Category"), "properties")["coordinators"]; ok {
		t.Error("[P2] Category must not embed coordinator assignments")
	}
}
