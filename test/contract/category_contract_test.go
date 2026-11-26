package contract

import (
	"slices"
	"testing"
)

// Acceptance coverage for event categories, AC 1-5 and 8.
//
// These read contracts/openapi.yaml directly, so they assert the SSOT itself
// rather than the generated output derived from it.
//
// Invoked by: api-ci -> acceptance job (`go test ./... -v` in test/contract).

const categoriesPath = "/api/v1/admin/events/{event_id}/categories"

func TestCategoryContract_NestedOperationsRequireBearerAuth(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	item := getMap(getMap(doc, "paths"), categoriesPath)
	if item == nil {
		t.Fatalf("missing category path %s", categoriesPath)
	}

	// A category has no identity without its event, so the operations are
	// nested. Both are Admin-only and must therefore declare bearer auth.
	for _, method := range []string{"get", "post"} {
		op := getMap(item, method)
		if op == nil {
			t.Errorf("missing %s %s", method, categoriesPath)
			continue
		}
		security, ok := op["security"].([]any)
		if !ok || len(security) == 0 {
			t.Errorf("%s %s must require bearer authentication", method, categoriesPath)
		}
	}

	if got := getStr(getMap(item, "get"), "operationId"); got != "listAdminEventCategories" {
		t.Errorf("list operationId = %q, want listAdminEventCategories", got)
	}
	if got := getStr(getMap(item, "post"), "operationId"); got != "createAdminEventCategory" {
		t.Errorf("create operationId = %q, want createAdminEventCategory", got)
	}
}

func TestCategoryContract_ExposesNoDeleteOrRename(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	paths := getMap(doc, "paths")

	// Scope is create + list only. Categories will be referenced by coordinator
	// assignments, OD records, disputes and tallies, so the same no-hard-delete
	// posture events already have applies from day one.
	item := getMap(paths, categoriesPath)
	for _, method := range []string{"delete", "put", "patch"} {
		if getMap(item, method) != nil {
			t.Errorf("%s %s must not exist", method, categoriesPath)
		}
	}
	if getMap(paths, "/api/v1/admin/events/{event_id}/categories/{category_id}") != nil {
		t.Error("no per-category path may exist: nothing addresses a category by id")
	}
}

func TestCategoryContract_SchemasAreGeneratedTypeBacked(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	for _, name := range []string{"Category", "CategoryCollection", "CreateCategoryRequest"} {
		if getMap(schemas, name) == nil {
			t.Errorf("components.schemas.%s is missing", name)
		}
	}

	categoryProps := getMap(getMap(schemas, "Category"), "properties")
	for _, field := range []string{"id", "event_id", "name", "created_at"} {
		if _, ok := categoryProps[field]; !ok {
			t.Errorf("Category missing %s", field)
		}
	}
	// event_id on the resource is what makes AC 3's scoping observable on the wire.
	if got := getStr(getMap(categoryProps, "event_id"), "format"); got != "uuid" {
		t.Errorf("Category.event_id format = %q, want uuid", got)
	}
	if got := getStr(getMap(categoryProps, "created_at"), "format"); got != "date-time" {
		t.Errorf("Category.created_at format = %q, want date-time", got)
	}

	create := getMap(getMap(schemas, "CreateCategoryRequest"), "properties")
	name := getMap(create, "name")
	if name == nil {
		t.Fatal("CreateCategoryRequest.name is missing")
	}
	if got := jsonNumber(name, "minLength"); got != 1 {
		t.Errorf("CreateCategoryRequest.name minLength = %v, want 1", got)
	}
	if got := jsonNumber(name, "maxLength"); got != 120 {
		t.Errorf("CreateCategoryRequest.name maxLength = %v, want 120", got)
	}
}

func TestCategoryContract_CollectionIsBoundedReferenceDataNotPaginated(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")
	collection := getMap(getMap(schemas, "CategoryCollection"), "properties")
	if collection == nil {
		t.Fatal("CategoryCollection is missing")
	}

	if _, ok := collection["data"]; !ok {
		t.Error("CategoryCollection must wrap its rows in data")
	}
	// Categories are bounded per-event reference data like BranchCollection, not
	// a paginated list like EventCollection. Pagination keys here would be a
	// wire-level promise the read path never keeps.
	for _, forbidden := range []string{"page", "page_size", "total"} {
		if _, ok := collection[forbidden]; ok {
			t.Errorf("CategoryCollection must not declare %s", forbidden)
		}
	}
}

func TestCategoryContract_ResponseFamilyCoversEveryRejection(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	item := getMap(getMap(doc, "paths"), categoriesPath)

	list := getMap(item, "get")
	for _, status := range []string{"200", "400", "401", "403", "404", "500"} {
		if jsonResponseSchema(doc, list, status) == nil {
			t.Errorf("GET %s must define a %s response", categoriesPath, status)
		}
	}

	create := getMap(item, "post")
	// 409 is what makes AC 4 observable on the wire; 400 must be the typed
	// ValidationError so a duplicate name arrives as a field error.
	for _, status := range []string{"201", "400", "401", "403", "404", "409", "500"} {
		if jsonResponseSchema(doc, create, status) == nil {
			t.Errorf("POST %s must define a %s response", categoriesPath, status)
		}
	}
	if ref := getStr(jsonResponseSchema(doc, create, "400"), "$ref"); ref != "#/components/schemas/ValidationError" {
		t.Errorf("POST %s 400 must use ValidationError, got %q", categoriesPath, ref)
	}
	if ref := getStr(jsonResponseSchema(doc, create, "201"), "$ref"); ref != "#/components/schemas/Category" {
		t.Errorf("POST %s 201 must return Category, got %q", categoriesPath, ref)
	}
}

func TestCategoryContract_EventArchivedJoinsTheErrorCodeEnum(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")
	code := getMap(getMap(getMap(schemas, "Error"), "properties"), "code")
	if code == nil {
		t.Fatal("Error.code is missing")
	}

	// The generated enum is the wire SSOT: api/internal/domain/errors.go maps it
	// to 409 and errors_test.go fails on any contract code without a mapping, so
	// the contract and Go must move together.
	if codes := enumStrings(code); !slices.Contains(codes, "EVENT_ARCHIVED") {
		t.Errorf("Error.code enum must contain EVENT_ARCHIVED, got %v", codes)
	}

	// The mark endpoint addresses categories by id, so the contract now carries
	// the truthful 404 code that handler can return for a missing category.
	if codes := enumStrings(code); !slices.Contains(codes, "CATEGORY_NOT_FOUND") {
		t.Error("Error.code enum must contain CATEGORY_NOT_FOUND")
	}
}

func TestCategoryContract_EventSchemaStaysUnchanged(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")
	event := getMap(schemas, "Event")

	// Event.required lists every property, so embedding categories would be a
	// breaking change for the Go/TS consumers and for the event tests, and it
	// would put an unbounded child collection inside a paginated list query.
	if _, ok := getMap(event, "properties")["categories"]; ok {
		t.Error("Event must not embed categories: the nested endpoint is the read path")
	}
}

// jsonNumber reads a numeric schema keyword regardless of whether the YAML
// decoder produced an int or a float64.
func jsonNumber(node map[string]any, key string) float64 {
	switch value := node[key].(type) {
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case float64:
		return value
	default:
		return -1
	}
}
