// Contract gate for the coordinator marking surface.
//
// These assert the SHAPE of the four marking read paths and their schemas. They
// assert nothing about behaviour — behaviour belongs to the service, handler and
// integration suites. Deliberately UNTAGGED: CI runs `go test ./...` here with no
// build tags, so a tagged file is a gate that never fires.
package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	markableEventsPath     = "/api/v1/events"
	markableCategoriesPath = "/api/v1/events/{event_id}/categories"
	studentSearchPath      = "/api/v1/students/search"
	markStatusPath         = "/api/v1/mark/status"
)

// 0.6-CONTRACT-001 — the scoped reads are SIBLINGS of the admin list, not edits
// to it. A dev who "reuses" /api/v1/admin/events by loosening its guard would
// pass a behaviour test and fail this one.
func TestMarkingSurfaceContract_ScopedEventAndCategoryReads(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	paths := getMap(doc, "paths")

	events := getMap(getMap(paths, markableEventsPath), "get")
	if events == nil {
		t.Fatalf("[P0] missing scoped event list GET %s", markableEventsPath)
	}
	if got := getStr(events, "operationId"); got != "listMarkableEvents" {
		t.Errorf("[P0] operationId = %q, want listMarkableEvents", got)
	}
	assertBearerSecurity(t, events, "GET "+markableEventsPath)
	if ref := getStr(jsonResponseSchema(doc, events, "200"), "$ref"); ref != "#/components/schemas/EventCollection" {
		t.Errorf("[P0] 200 schema = %q, want EventCollection (reuse, do not fork)", ref)
	}

	categories := getMap(getMap(paths, markableCategoriesPath), "get")
	if categories == nil {
		t.Fatalf("[P0] missing scoped category list GET %s", markableCategoriesPath)
	}
	if got := getStr(categories, "operationId"); got != "listMarkableEventCategories" {
		t.Errorf("[P0] operationId = %q, want listMarkableEventCategories", got)
	}
	assertBearerSecurity(t, categories, "GET "+markableCategoriesPath)
	if ref := getStr(jsonResponseSchema(doc, categories, "200"), "$ref"); ref != "#/components/schemas/CategoryCollection" {
		t.Errorf("[P0] 200 schema = %q, want CategoryCollection (reuse, do not fork)", ref)
	}

	// The admin surface must survive this story untouched.
	if admin := getMap(getMap(paths, "/api/v1/admin/events"), "get"); admin == nil {
		t.Error("[P0] /api/v1/admin/events GET disappeared — the scoped read is a sibling, not a replacement")
	} else if got := getStr(admin, "operationId"); got != "listAdminEvents" {
		t.Errorf("[P0] admin event list operationId changed to %q — 0.6 must not touch it", got)
	}
}

// 0.6-CONTRACT-002 — the search response is the story's sharpest secrecy
// boundary: `students` has no email column and card tokens are stored as
// fingerprints only. This asserts that structurally so a future "convenience"
// field cannot be added without a failing test.
func TestMarkingSurfaceContract_StudentSearchExposesNoSecrets(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	search := getMap(getMap(getMap(doc, "paths"), studentSearchPath), "get")
	if search == nil {
		t.Fatalf("[P0] missing student search path GET %s", studentSearchPath)
	}
	if got := getStr(search, "operationId"); got != "searchStudents" {
		t.Errorf("[P0] operationId = %q, want searchStudents", got)
	}
	assertBearerSecurity(t, search, "GET "+studentSearchPath)

	params, _ := search["parameters"].([]any)
	found := map[string]map[string]any{}
	for _, raw := range params {
		p := asMap(raw)
		found[getStr(p, "name")] = p
	}

	q, ok := found["q"]
	if !ok {
		t.Fatal("[P0] search is missing the required `q` query parameter")
	}
	if required, _ := q["required"].(bool); !required {
		t.Error("[P0] `q` must be required — an unbounded empty search is a roster dump")
	}
	qSchema := resolveSchema(doc, getMap(q, "schema"))
	if got := getFloat(qSchema, "minLength"); got != 3 {
		t.Errorf("[P0] q.minLength = %v, want 3", got)
	}
	if got := getFloat(qSchema, "maxLength"); got != 64 {
		t.Errorf("[P0] q.maxLength = %v, want 64", got)
	}

	limit, ok := found["limit"]
	if !ok {
		t.Fatal("[P0] search is missing the `limit` query parameter")
	}
	limitSchema := resolveSchema(doc, getMap(limit, "schema"))
	if got := getFloat(limitSchema, "minimum"); got != 1 {
		t.Errorf("[P0] limit.minimum = %v, want 1", got)
	}
	if got := getFloat(limitSchema, "maximum"); got != 50 {
		t.Errorf("[P0] limit.maximum = %v, want 50", got)
	}
	if got := getFloat(limitSchema, "default"); got != 20 {
		t.Errorf("[P0] limit.default = %v, want 20", got)
	}

	// Walk the actual response item and refuse anything that could leak.
	item := searchResultItemSchema(t, doc, search)
	props := schemaProps(doc, item)
	for _, banned := range []string{"email", "token", "card_token", "token_fingerprint", "fingerprint", "card"} {
		if _, present := props[banned]; present {
			t.Errorf("[P0] search result item exposes %q — students has no email column and tokens are fingerprint-only (AR20/NFR16)", banned)
		}
	}
	for _, want := range []string{"student_id", "roll_no", "display_name"} {
		if _, present := props[want]; !present {
			t.Errorf("[P0] search result item is missing %q — the coordinator picks a student by roll and name", want)
		}
	}
}

// 0.6-CONTRACT-003 — counters and the slot clock are one endpoint, and `slot`
// is nullable rather than a sentinel 0. Encoding "no slot" as 0 would let a
// client render a countdown for a slot that does not exist.
func TestMarkingSurfaceContract_MarkStatusShape(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	status := getMap(getMap(getMap(doc, "paths"), markStatusPath), "get")
	if status == nil {
		t.Fatalf("[P0] missing mark status path GET %s", markStatusPath)
	}
	if got := getStr(status, "operationId"); got != "getMarkStatus" {
		t.Errorf("[P0] operationId = %q, want getMarkStatus", got)
	}
	assertBearerSecurity(t, status, "GET "+markStatusPath)

	params, _ := status["parameters"].([]any)
	required := map[string]bool{}
	for _, raw := range params {
		p := asMap(raw)
		isRequired, _ := p["required"].(bool)
		required[getStr(p, "name")] = isRequired
	}
	for _, name := range []string{"event_id", "category_id"} {
		if !required[name] {
			t.Errorf("[P0] %s must be a required query parameter — counters are always scoped", name)
		}
	}

	if ref := getStr(jsonResponseSchema(doc, status, "200"), "$ref"); ref != "#/components/schemas/MarkStatus" {
		t.Errorf("[P0] 200 schema = %q, want MarkStatus", ref)
	}

	schemas := getMap(getMap(doc, "components"), "schemas")

	markStatus := getMap(schemas, "MarkStatus")
	if markStatus == nil {
		t.Fatal("[P0] components.schemas.MarkStatus is missing")
	}
	// `slot` is REQUIRED as well as nullable, and that is load-bearing rather
	// than pedantic. Merely optional makes the Go generator emit
	// `json:"slot,omitempty"`, so the key VANISHES from the response whenever no
	// slot is open — and a client then cannot distinguish "no slot is open" from
	// "this server does not report slots". Required-and-nullable is the correct
	// encoding for "always present, may be null", and it is what makes the
	// explicit no-slot state observable at all.
	//
	// This assertion is the gate on that decision. Without `slot` in this list,
	// dropping it from `required` regenerated the omitempty form and reintroduced
	// the exact bug the deviation exists to prevent, with nothing failing.
	if missing := missingRequired(markStatus, "server_time", "slot", "counters"); len(missing) > 0 {
		t.Errorf("[P0] MarkStatus required is missing %v — `slot` must be required AND nullable, "+
			"or the generator emits omitempty and the no-slot state becomes indistinguishable from an old server", missing)
	}
	statusProps := getMap(markStatus, "properties")
	slot := getMap(statusProps, "slot")
	if slot == nil {
		t.Fatal("[P0] MarkStatus.slot is missing")
	}
	if nullable, _ := slot["nullable"].(bool); !nullable {
		t.Error("[P0] MarkStatus.slot must be nullable: `slot: null` is the explicit no-slot-open state, never slot 0")
	}
	if _, hasAllOf := slot["allOf"]; !hasAllOf {
		t.Error("[P0] MarkStatus.slot must use the 3.0.3 nullable-$ref idiom (nullable + allOf); a bare $ref sibling to nullable is ignored by the generator")
	}

	window := getMap(schemas, "MarkSlotWindow")
	if window == nil {
		t.Fatal("[P0] components.schemas.MarkSlotWindow is missing")
	}
	if missing := missingRequired(window, "slot", "started_at", "ends_at"); len(missing) > 0 {
		t.Errorf("[P0] MarkSlotWindow required is missing %v", missing)
	}
	slotNum := resolveSchema(doc, getMap(getMap(window, "properties"), "slot"))
	if got := getFloat(slotNum, "minimum"); got != 1 {
		t.Errorf("[P0] MarkSlotWindow.slot.minimum = %v, want 1 (slot 0 is not a slot)", got)
	}
	if got := getFloat(slotNum, "maximum"); got != MaxSlot {
		t.Errorf("[P0] MarkSlotWindow.slot.maximum = %v, want %d", got, MaxSlot)
	}

	counters := getMap(schemas, "MarkCounters")
	if counters == nil {
		t.Fatal("[P0] components.schemas.MarkCounters is missing")
	}
	if missing := missingRequired(counters, "mine", "category_total"); len(missing) > 0 {
		t.Errorf("[P0] MarkCounters required is missing %v — FR13 needs BOTH the personal and the combined figure", missing)
	}
	counterProps := getMap(counters, "properties")
	for _, name := range []string{"mine", "category_total"} {
		field := resolveSchema(doc, getMap(counterProps, name))
		if got := getStr(field, "type"); got != "integer" {
			t.Errorf("[P0] MarkCounters.%s type = %q, want integer", name, got)
		}
		if got := getFloat(field, "minimum"); got != 0 {
			t.Errorf("[P0] MarkCounters.%s minimum = %v, want 0", name, got)
		}
	}
}

// 0.6-CONTRACT-004 — Task 3 widens ONE branch of the pairing rule. This pins
// the two things that must NOT move with it: the 202 scan path, and the closed
// error registry.
func TestMarkingSurfaceContract_PairingDocumentedAndRegistriesStable(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	request := getMap(schemas, "MarkAttendanceRequest")
	if request == nil {
		t.Fatal("[P0] components.schemas.MarkAttendanceRequest is missing")
	}
	// The description is load-bearing documentation: the camera path resolves to
	// a student_id because the printed barcode carries a ROLL NUMBER, not a
	// sealed token. A reader of the spec must not have to infer that.
	if desc := getStr(request, "description"); desc == "" {
		t.Error("[P0] MarkAttendanceRequest needs a description stating the identity/capture pairing that the service enforces")
	} else if !containsAllFold(desc, "camera", "student_id") {
		t.Errorf("[P0] MarkAttendanceRequest description must state that student_id pairs with camera as well as search; got %q", desc)
	}
	props := getMap(request, "properties")
	odDate := resolveSchema(doc, getMap(props, "od_date"))
	if odDate == nil {
		t.Fatal("[P0] MarkAttendanceRequest must include optional od_date so teacher/admin search marks can file a selected day")
	}
	if got := getStr(odDate, "format"); got != "date" {
		t.Errorf("[P0] MarkAttendanceRequest.od_date format = %q, want date", got)
	}

	// /api/v1/scans is Epic 3's deferred 202 path. Epic 0 is synchronous only.
	if scans := getMap(getMap(doc, "paths"), "/api/v1/scans"); scans != nil {
		if post := getMap(scans, "post"); post != nil {
			if ref := getStr(jsonResponseSchema(doc, post, "202"), "$ref"); ref == "" {
				t.Error("[P0] /api/v1/scans lost its 202 response — Story 0.6 must not touch the deferred batch path")
			}
		}
	}

	// AC 5's red states are all covered by codes that already exist.
	errorCode := getMap(schemas, "ErrorCode")
	if errorCode == nil {
		errorCode = findSchemaWithProps(doc, "code")
	}
	existing := enumStrings(errorCode)
	if len(existing) == 0 {
		t.Skip("error code enum not discoverable in this spec shape; covered by contract_acceptance_test.go")
	}
	for _, want := range []string{
		"SCAN_DUPLICATE", "SLOT_CLOSED", "CARD_NOT_RECOGNIZED", "EVENT_NOT_FOUND",
		"EVENT_ARCHIVED", "CATEGORY_NOT_FOUND", "STUDENT_NOT_FOUND", "RBAC_FORBIDDEN", "VALIDATION_FAILED",
	} {
		if missing := containsAll(existing, []string{want}); len(missing) > 0 {
			t.Errorf("[P0] error code %s is missing — AC 5 relies on the EXISTING registry; 0.6 should need no new code", want)
		}
	}
}

// searchResultItemSchema digs the per-student item out of whatever collection
// wrapper the spec uses, so the secrecy assertion above runs against the real
// item rather than the envelope.
func searchResultItemSchema(t *testing.T, doc, op map[string]any) map[string]any {
	t.Helper()
	body := resolveSchema(doc, jsonResponseSchema(doc, op, "200"))
	if body == nil {
		t.Fatal("[P0] search has no 200 application/json schema")
	}
	data := resolveSchema(doc, getMap(getMap(body, "properties"), "data"))
	if data == nil {
		t.Fatal("[P0] search 200 body has no `data` collection property")
	}
	item := resolveSchema(doc, getMap(data, "items"))
	if item == nil {
		t.Fatal("[P0] search 200 `data` has no item schema")
	}
	return item
}

// containsAllFold reports whether every needle appears in haystack, case-insensitively.
func containsAllFold(haystack string, needles ...string) bool {
	lower := make([]rune, 0, len(haystack))
	for _, r := range haystack {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		lower = append(lower, r)
	}
	hay := string(lower)
	for _, needle := range needles {
		if !containsFold(hay, needle) {
			return false
		}
	}
	return true
}

func containsFold(hay, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			a, b := hay[i+j], needle[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// The generated Go struct is where the `slot` decision actually bites, so it is
// asserted directly rather than only through the spec.
//
// The spec assertion above and this one guard the same property from both ends:
// the YAML says `slot` is required, and the emitted Go tag must therefore be
// `json:"slot"` with NO omitempty. If a future edit drops `slot` from required,
// the generator silently emits `json:"slot,omitempty"` and the key disappears
// from the wire whenever no slot is open — which is precisely the state the
// countdown has to render as "no slot is open right now".
func TestGeneratedMarkStatusKeepsSlotOnTheWire(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "contracts", "generated", "go", "types.gen.go"))
	if err != nil {
		t.Fatalf("read generated types: %v", err)
	}

	block := structBlock(string(source), "MarkStatus")
	if block == "" {
		t.Fatal("[P0] type MarkStatus not found in the generated Go types")
	}
	if !strings.Contains(block, "`json:\"slot\"`") {
		t.Errorf("[P0] MarkStatus.Slot is not tagged `json:\"slot\"`. Generated block:\n%s\n"+
			"An omitempty here removes the key entirely when no slot is open, and a client cannot then "+
			"tell \"no slot\" from \"this server does not report slots\".", block)
	}
	if strings.Contains(block, "slot,omitempty") {
		t.Errorf("[P0] MarkStatus.Slot carries omitempty — `slot` must be required AND nullable in the spec. Block:\n%s", block)
	}
}

// structBlock returns the body of `type <name> struct { ... }`, or "".
func structBlock(source, name string) string {
	start := strings.Index(source, "type "+name+" struct {")
	if start < 0 {
		return ""
	}
	end := strings.Index(source[start:], "\n}")
	if end < 0 {
		return ""
	}
	return source[start : start+end]
}

// Every `slot` bound in the spec agrees with MaxSlot.
//
// WHY THIS EXISTS. The ceiling appears on a dozen schemas and query parameters —
// the mark request, the status window, the dispute, the attendance filters, the
// public board. Widening it means editing all of them, and the failure mode of
// missing one is silent: the API accepts a slot that one client's generated
// types say is out of range, or a picker offers a slot another endpoint refuses.
//
// Two hardcoded expectations in two files could not catch that, and did not:
// when the evening slots landed, one inline `maximum: 12` survived a
// find-and-replace and nothing failed. This walks the whole document instead, so
// a partial widening is a red build rather than a defect somebody meets during
// an event.
func TestMarkingSurfaceContract_EverySlotBoundAgreesWithTheCeiling(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))

	checked := 0
	var walk func(path string, node any)
	walk = func(path string, node any) {
		switch typed := node.(type) {
		case map[string]any:
			// TWO SHAPES carry a slot bound, and missing the second is how the
			// first version of this test passed a deliberate mutation:
			//
			//   a PROPERTY   — `slot: {type: integer, maximum: N}`
			//   a PARAMETER  — `{name: slot, in: query, schema: {...}}`
			//
			// In the parameter form the key is `schema` and the word "slot" is a
			// VALUE, so a walk keyed on the property name never sees it — and the
			// query filters are exactly where a partial widening hides.
			if getStr(typed, "name") == "slot" {
				if schema := resolveSchema(doc, getMap(typed, "schema")); schema != nil {
					assertSlotBound(t, path+"(param)", schema, &checked)
				}
			}
			for key, child := range typed {
				if key == "slot" {
					if schema := asMap(child); schema != nil && getStr(schema, "type") == "integer" {
						assertSlotBound(t, path+".slot", schema, &checked)
					}
				}
				walk(path+"."+key, child)
			}
		case []any:
			for index, child := range typed {
				walk(fmt.Sprintf("%s[%d]", path, index), child)
			}
		}
	}
	walk("spec", doc)

	// Without this the walk is skippable: a spec that renamed the field, or a
	// traversal that never recursed, would report success having checked nothing.
	if checked < 8 {
		t.Errorf("[P0] only %d bounded slot schemas were found; the spec carries one on the mark request, the "+
			"status window, the dispute, the attendance filters and the public board, so a number this low "+
			"means the walk stopped finding them rather than that they all agree", checked)
	}
}

// assertSlotBound holds one slot schema to the ceiling.
func assertSlotBound(t *testing.T, where string, schema map[string]any, checked *int) {
	t.Helper()
	if _, bounded := schema["maximum"]; !bounded {
		return
	}
	*checked++
	if got := getFloat(schema, "maximum"); got != MaxSlot {
		t.Errorf("[P0] %s.maximum = %v, want %d — one slot bound was widened and this one was not, so a client "+
			"generated from this spec disagrees with the API about which slots exist", where, got, MaxSlot)
	}
	if got := getFloat(schema, "minimum"); got != 1 {
		t.Errorf("[P0] %s.minimum = %v, want 1 (slot 0 is not a slot)", where, got)
	}
}
