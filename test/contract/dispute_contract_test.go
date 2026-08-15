// Contract gates for Story 0.11's dispute surface — the FIRST unauthenticated
// WRITE in this codebase.
//
// RED PHASE. Like the 0.10 file next to it, these parse contracts/openapi.yaml
// as YAML rather than importing generated Go types, so this file COMPILES today
// and fails on assertions that name the missing thing. The contract is the first
// artifact the dev touches, and a package that fails to build reports one opaque
// error for the whole suite.
//
// 0.10 proved a public READ discloses nothing. The assertions here exist because
// a public WRITE has three failure modes a read does not have:
//
//   - it can be a directory oracle (AC 3 — the refusal must be identical for a
//     bad domain and an unknown address, so the RESPONSE FAMILY may not branch);
//   - it can echo the submitter's email back onto an open-internet response
//     (AC 10 — hence the receipt allowlist);
//   - it can be walked by anyone on the internet (AC 9 — hence 429 documented on
//     the write, and no 401/403 documented at all).
//
// DELIBERATELY NOT GATED behind an env flag or a build tag (0.9 shipped an entire
// suite behind ATDD_ACTIVE_09, which nothing ever set). Helpers — loadSpec,
// repoRoot, getMap, getStr, getFloat, asMap, resolveSchema, assertResponseFamily,
// missingRequired, indexParams, sortedKeys — come from the sibling files in this
// package.
package contract

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

const (
	publicDisputePath      = "/api/v1/public/disputes"
	publicEventsPath       = "/api/v1/public/events"
	disputeListPath        = "/api/v1/disputes"
	disputeResolutionPath  = "/api/v1/disputes/{dispute_id}/resolution"
	publicStudentSchemaRef = "#/components/schemas/PublicStudent"
)

// publicDisputeSchemaAllowlists is the load-bearing table of this file, and it is
// an ALLOWLIST for the same reason 0.10's is: a blocklist of sensitive names
// passes trivially for a schema that never declared them, and the NEXT field
// somebody adds is not on the list either.
//
// The receipt is the sharpest entry. FR42 requires the server-resolved student
// display fields to come back so the visitor can see the auto-population worked —
// which makes the receipt the one public response in the system built from an
// identity the caller asserted. `submitted_email` is deliberately ABSENT from it
// (AC 10): echoing the address back turns the receipt into a confirmation that
// the address resolved, which is precisely the oracle T3 spends a whole locked
// decision closing.
//
// `od_date` is on the request because of the 2026-08-06 multi-day amendment
// (docs/multi-day-events-plan.md §7), not because of the original spec: the
// 0.12a turns an event into a real date range, after which "I was missed in slot
// 8" is ambiguous across thirty days. The column has to exist when the table is
// CREATED — a nullable date added later never collides in a Postgres unique index,
// so duplicates come back silently, and by then the table holds real student
// submissions.
var publicDisputeSchemaAllowlists = map[string][]string{
	"PublicDisputeRequest": {"student_id", "submitted_email", "event_id", "category_id", "od_date", "slot", "claimed_coordinator_name", "student_note"},
	"PublicDisputeReceipt": {"dispute_id", "reference", "status", "submitted_at", "student"},
	// `end_date` was added under Story 0.12a, honouring the instruction this
	// comment used to carry: an event is now a real date range, and the dispute
	// form's day picker enumerates event_date..end_date. Without the range on the
	// wire the picker has exactly one option and a student missed on day 17 of a
	// festival cannot report it at all.
	//
	// It discloses nothing a visitor could not already read: the event's start
	// date has been on this schema since 0.11, and the end date of a public campus
	// festival is not a secret. What it DOES do is bound the day control — which is
	// the point of T23, since an unbounded <input type="date"> lets an anonymous
	// submitter file against any day in history.
	"PublicEvent":           {"event_id", "name", "event_date", "end_date", "categories"},
	"PublicEventCategory":   {"id", "name"},
	"PublicEventCollection": {"data"},
}

// 0.11-CONTRACT-001 — the public dispute WRITE and the public event READ declare
// no `security`, and neither documents a 401 or a 403.
//
// The spec declares no root-level `security`, so under OpenAPI 3.0.3 the ABSENCE
// of the key is what makes an operation unauthenticated (0.10 made that
// load-bearing). Copying an authenticated operation as a starting point and
// forgetting to delete its `security` block produces a form that 401s every
// student — the same class of defect as 0.9's unwired guard, one file earlier.
func TestDisputeContract_PublicOperationsAreUnauthenticated(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))

	if _, declared := doc["security"]; declared {
		t.Fatalf("[P0] the spec now declares a ROOT-level `security`; omitting `security` on an operation no " +
			"longer means unauthenticated, and both public dispute operations would inherit the global requirement")
	}

	paths := getMap(doc, "paths")
	for _, tc := range []struct{ method, path, operationID string }{
		{"post", publicDisputePath, "submitPublicDispute"},
		{"get", publicEventsPath, "listPublicEvents"},
	} {
		op := getMap(getMap(paths, tc.path), tc.method)
		if op == nil {
			t.Errorf("[P0] missing public operation %s %s — a student who was never marked has no way to report it",
				strings.ToUpper(tc.method), tc.path)
			continue
		}
		if _, declared := op["security"]; declared {
			t.Errorf("[P0] %s %s declares `security` — every submitter is anonymous, so this operation would 401 "+
				"for its entire audience. Omit the key; do not write `security: []`, which reads as an override of "+
				"a global requirement that does not exist here", strings.ToUpper(tc.method), tc.path)
		}
		if got := getStr(op, "operationId"); got != tc.operationID {
			t.Errorf("[P0] %s %s operationId = %q, want %q", strings.ToUpper(tc.method), tc.path, got, tc.operationID)
		}
		for _, forbidden := range []string{"401", "403"} {
			if getMap(getMap(op, "responses"), forbidden) != nil {
				t.Errorf("[P0] %s %s documents a %s response; there is no credential to be missing and no role to "+
					"be insufficient, and a documented 401 becomes a generated client with a sign-out branch on a "+
					"public page", strings.ToUpper(tc.method), tc.path, forbidden)
			}
		}
		if getMap(getMap(op, "responses"), "429") == nil {
			t.Errorf("[P0] %s %s does not document 429. The write sits behind its OWN stricter limiter (T14) and "+
				"the group limiter above it, so RATE_LIMITED is a refusal the UI must have a typed branch for (AC 9)",
				strings.ToUpper(tc.method), tc.path)
		}
	}
}

// 0.11-CONTRACT-002 — the public write's request and response family.
//
// The 404 is CATEGORY/EVENT, the 409 is DISPUTE_ALREADY_OPEN, the 400 covers
// both edge validation and the identity refusal. Pinning the family here is what
// stops the identity refusal quietly acquiring its own status code later, which
// would reintroduce the oracle by the back door: a 404 for "no such student" and
// a 400 for "bad domain" discloses exactly what T3 forbids, even if both bodies
// say the same words.
func TestDisputeContract_PublicWriteShapeAndResponseFamily(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	op := getMap(getMap(getMap(doc, "paths"), publicDisputePath), "post")
	if op == nil {
		t.Fatalf("[P0] missing public operation POST %s", publicDisputePath)
	}

	if params := indexParams(op); len(params) > 0 {
		for name := range params {
			t.Errorf("[P1] POST %s accepts parameter %q; every accepted input on an open endpoint is a bound "+
				"somebody has to defend, and the whole submission belongs in the bounded JSON body", publicDisputePath, name)
		}
	}

	body := getMap(getMap(getMap(getMap(op, "requestBody"), "content"), "application/json"), "schema")
	if got := getStr(body, "$ref"); got != "#/components/schemas/PublicDisputeRequest" {
		t.Errorf("[P0] POST %s request body $ref = %q, want #/components/schemas/PublicDisputeRequest",
			publicDisputePath, got)
	}
	if required, _ := getMap(op, "requestBody")["required"].(bool); !required {
		t.Errorf("[P0] POST %s requestBody is not `required: true`; an absent body must be a typed "+
			"VALIDATION_FAILED at the edge, never a nil-deref in the handler (AC 4)", publicDisputePath)
	}

	assertResponseFamily(t, doc, op, map[string]string{
		"201": "#/components/schemas/PublicDisputeReceipt",
		"404": "#/components/schemas/Error",
		"409": "#/components/schemas/Error",
		"429": "#/components/schemas/Error",
		"500": "#/components/schemas/Error",
	})

	// The 400 is a UNION, and it has to be. This operation emits two different
	// 400 bodies: a field failure (VALIDATION_FAILED, with typed details) and the
	// non-disclosing identity refusal (DISPUTE_IDENTITY_UNVERIFIED, with NO
	// details, so the three refusal paths stay byte-identical). Declaring
	// ValidationError alone was untrue of the second: that schema pins
	// `code` to the single value VALIDATION_FAILED and REQUIRES `details`, so
	// every identity refusal the API actually sends failed its own contract.
	badRequest := jsonResponseSchema(doc, op, "400")
	variants, _ := badRequest["oneOf"].([]any)
	if len(variants) != 2 {
		t.Fatalf("[P0] status 400 on POST %s must be a oneOf of ValidationError and Error, got %v. "+
			"DISPUTE_IDENTITY_UNVERIFIED cannot validate against ValidationError, whose code enum is "+
			"[VALIDATION_FAILED] and whose `details` is required (T3, AC 3)", publicDisputePath, badRequest)
	}
	wantVariants := map[string]bool{
		"#/components/schemas/ValidationError": false,
		"#/components/schemas/Error":           false,
	}
	for _, raw := range variants {
		ref := getStr(asMap(raw), "$ref")
		if _, ok := wantVariants[ref]; !ok {
			t.Errorf("[P0] status 400 oneOf carries unexpected variant %q", ref)
			continue
		}
		wantVariants[ref] = true
	}
	for ref, seen := range wantVariants {
		if !seen {
			t.Errorf("[P0] status 400 oneOf is missing %q", ref)
		}
	}

	// 201, not 200. The receipt names a row that now exists, and NFR50's
	// confirmation copy is the visitor's only evidence of it — there is no
	// notification and no status endpoint (FR59 is dropped), so the response to
	// this one request is the entire feedback channel.
	if getMap(getMap(op, "responses"), "200") != nil {
		t.Errorf("[P0] POST %s documents a 200; a created dispute is a 201 with a receipt", publicDisputePath)
	}
}

// 0.11-CONTRACT-003 — the public event read takes NO parameters at all (T7).
//
// A knob the open internet controls is a knob that gets turned to 10,000. The
// cap is fixed server-side and the ordering is fixed server-side; there is no
// `limit`, no `page`, no `status` and no `q`. This is the same rule 0.10 applied
// to the public search, restated because the temptation here is stronger — the
// authenticated /api/v1/admin/events has page, page_size AND status, and it is
// the obvious operation to copy.
func TestDisputeContract_PublicEventsAcceptsNoCallerKnobs(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	op := getMap(getMap(getMap(doc, "paths"), publicEventsPath), "get")
	if op == nil {
		t.Fatalf("[P0] missing public operation GET %s", publicEventsPath)
	}

	for name := range indexParams(op) {
		t.Errorf("[P0] GET %s accepts parameter %q. T7 fixes the cap and the ordering server-side and accepts "+
			"NOTHING from the caller — a `status` filter in particular would let a caller ask which events are "+
			"archived, which is administrative state a student has no business reading", publicEventsPath, name)
	}

	assertResponseFamily(t, doc, op, map[string]string{
		"200": "#/components/schemas/PublicEventCollection",
		"429": "#/components/schemas/Error",
		"500": "#/components/schemas/Error",
	})
}

// 0.11-CONTRACT-004 — every public dispute schema's property set matches its
// allowlist EXACTLY (AC 8, AC 10).
func TestDisputeContract_PublicSchemaAllowlists(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	for _, name := range sortedKeys(publicDisputeSchemaAllowlists) {
		allowed := publicDisputeSchemaAllowlists[name]
		schema := getMap(schemas, name)
		if schema == nil {
			t.Errorf("[P0] components.schemas.%s is missing", name)
			continue
		}
		props := getMap(schema, "properties")
		allow := map[string]bool{}
		for _, field := range allowed {
			allow[field] = true
			if props[field] == nil {
				t.Errorf("[P0] %s is missing declared property %s", name, field)
			}
		}
		for field := range props {
			if !allow[field] {
				t.Errorf("[P0] %s exposes %q, which is not on the public allowlist. This schema crosses an "+
					"UNAUTHENTICATED boundary in both directions: adding a field is a disclosure decision on the "+
					"way out and an attack-surface decision on the way in. Add it to "+
					"publicDisputeSchemaAllowlists in the same change and say why", name, field)
			}
		}
	}

	// The receipt must not carry the submitted email back (AC 10). Asserted
	// separately from the allowlist loop so the failure names the reason rather
	// than reporting a generic extra property.
	receipt := getMap(schemas, "PublicDisputeReceipt")
	if receipt != nil {
		if getMap(receipt, "properties")["submitted_email"] != nil {
			t.Errorf("[P0] PublicDisputeReceipt echoes submitted_email. A receipt that repeats the address back " +
				"is a confirmation that the address resolved — the exact directory oracle T3 closes by making the " +
				"three refusal paths byte-identical. The visitor typed it; they do not need it read back to them")
		}
		// The student block is the ONLY place FR42's auto-population is visible,
		// so it has to be the PUBLIC student shape rather than a fresh inline
		// object somebody grows a field on later.
		if got := getStr(getMap(getMap(receipt, "properties"), "student"), "$ref"); got != publicStudentSchemaRef {
			t.Errorf("[P0] PublicDisputeReceipt.student $ref = %q, want %s — reuse 0.10's public student shape, "+
				"which already has an exact allowlist guarding it, rather than declaring a second inline object "+
				"nobody is watching", got, publicStudentSchemaRef)
		}
	}

	// Bounds declared IN THE CONTRACT, not only in Go. The generated client is
	// what the browser sends through, and a bound that exists only server-side
	// means the first feedback a student gets is a 400 (AC 4).
	request := getMap(schemas, "PublicDisputeRequest")
	if request != nil {
		props := getMap(request, "properties")
		for _, tc := range []struct {
			field string
			key   string
			want  float64
			why   string
		}{
			{"submitted_email", "maxLength", 320, "RFC-bounded address length; anything longer is not an address"},
			{"student_note", "minLength", 1, "all public dispute fields are compulsory"},
			{"student_note", "maxLength", 1000, "the student's own note"},
			{"claimed_coordinator_name", "minLength", 1, "all public dispute fields are compulsory"},
			{"claimed_coordinator_name", "maxLength", 200, "free text a student types from memory"},
			{"slot", "minimum", 1, "slot 0 is not a slot"},
			{"slot", "maximum", MaxSlot, "the slot table's ceiling"},
		} {
			schema := resolveSchema(doc, getMap(props, tc.field))
			if schema == nil {
				continue // the missing-property loop above already reported it
			}
			if got := getFloat(schema, tc.key); got != tc.want {
				t.Errorf("[P0] PublicDisputeRequest.%s.%s = %v, want %v (%s)", tc.field, tc.key, got, tc.want, tc.why)
			}
		}

		// All public dispute form fields are compulsory. The request must not
		// let the browser omit coordinator name or the student's note and then
		// discover the problem only in the service.
		if required := requiredSet(request); !required["claimed_coordinator_name"] || !required["student_note"] {
			t.Errorf("[P0] PublicDisputeRequest must require claimed_coordinator_name and student_note; required = %v", required)
		}
		// The OD date is REQUIRED and is a date, not a datetime. It is the day the
		// OD belongs to, which under 0.12a is one of up to thirty days in an
		// event's range — a timestamp would carry a timezone the campus slot table
		// does not have, and an optional one would be NULL, which never collides
		// in the partial unique index and lets duplicates back in.
		odDate := resolveSchema(doc, getMap(props, "od_date"))
		if odDate == nil {
			t.Errorf("[P0] PublicDisputeRequest has no od_date. Amended 2026-08-06 by the multi-day plan " +
				"(docs/multi-day-events-plan.md §7): `disputes` must carry `od_date date NOT NULL` from CREATION, because the range change " +
				"makes an event span a date range and \"I was missed in slot 8\" is then ambiguous across thirty " +
				"days. The story FILE predates this amendment — the planning artifacts win")
		} else if got := getStr(odDate, "format"); got != "date" {
			t.Errorf("[P0] od_date.format = %q, want \"date\" — the day an OD belongs to, not an instant", got)
		}
		if required := requiredSet(request); !required["od_date"] {
			t.Errorf("[P0] PublicDisputeRequest does not require od_date. Postgres treats NULLs as DISTINCT in a " +
				"unique index, so a single nullable-dated row never collides and the duplicate guard silently " +
				"stops guarding")
		}

		if required := requiredSet(request); !required["student_id"] || !required["submitted_email"] {
			t.Errorf("[P0] PublicDisputeRequest must require BOTH student_id and submitted_email. The email is " +
				"FR42's lookup key; the student_id is the binding that stops the form being used to file disputes " +
				"for arbitrary others, and the two are cross-checked (T5)")
		}
	}
}

// 0.11-CONTRACT-005 — no public schema reaches an AUTHENTICATED schema by $ref,
// including the three this story adds.
//
// The authenticated `Dispute` carries the resolution note, the resolving user id
// and the submitted email. It overlaps the public receipt today, which is exactly
// what makes sharing it tempting and what makes sharing it fatal: the next field
// added for the teacher inbox would ship to the open internet with nobody
// reviewing that decision. The whole subtree is marshalled and searched, so a
// $ref nested inside an array item is caught too.
func TestDisputeContract_PublicDisputeSchemasNeverRefAnAuthenticatedSchema(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	forbidden := []string{
		"Dispute", "DisputeCollection", "DisputeResolutionRequest",
		"AttendanceActor", "AttendanceRecord", "AttendanceRecordCollection",
		"Event", "EventCollection", "Category", "CategoryCollection",
		"CoordinatorAssignment", "CoordinatorAssignmentCollection",
		"AuthenticatedIdentity", "StudentSearchResults", "CardSeal",
	}

	for _, name := range sortedKeys(publicDisputeSchemaAllowlists) {
		schema := getMap(schemas, name)
		if schema == nil {
			continue
		}
		encoded, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		body := string(encoded)
		for _, target := range forbidden {
			// The trailing quote guards against a prefix match: Dispute is a
			// prefix of DisputeCollection, and a bare substring hit on the
			// shorter name would report the wrong schema.
			if strings.Contains(body, fmt.Sprintf("#/components/schemas/%s\"", target)) {
				t.Errorf("[P0] %s $refs the authenticated schema %s. Public schemas are DISTINCT types (0.10's T3) "+
					"— %s in particular carries the resolution note, the resolving user id and the submitted "+
					"email, none of which may ever appear on an unauthenticated response (AC 10)",
					name, target, target)
			}
		}
	}
}

// 0.11-CONTRACT-006 — the AUTHENTICATED dispute operations do require a bearer
// token, asserted PER OPERATION rather than per path.
//
// 0.10's review caught the path-based version of this check: exempting or
// asserting by path means a second method later dropped onto the same path
// inherits the verdict of the first. `/api/v1/disputes` is a real candidate for
// exactly that — the natural place somebody would later add an authenticated
// POST — so the assertion is keyed on the operation.
func TestDisputeContract_AuthenticatedDisputeOperationsRequireBearer(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	paths := getMap(doc, "paths")

	inspected := 0
	for _, tc := range []struct{ method, path string }{
		{"get", disputeListPath},
		{"post", disputeResolutionPath},
	} {
		op := getMap(getMap(paths, tc.path), tc.method)
		if op == nil {
			t.Errorf("[P0] missing authenticated operation %s %s — a teacher cannot read or resolve a dispute "+
				"without it (FR36, FR37)", strings.ToUpper(tc.method), tc.path)
			continue
		}
		inspected++

		security, ok := op["security"].([]any)
		if !ok || len(security) == 0 {
			t.Errorf("[P0] %s %s declares no `security`. Since 0.10 that omission IS the machine-readable "+
				"declaration that an operation is open to the internet — and this one returns another student's "+
				"submitted email, note and resolution history to whoever asks", strings.ToUpper(tc.method), tc.path)
			continue
		}
		if !strings.Contains(fmt.Sprint(security), "bearerAuth") {
			t.Errorf("[P0] %s %s declares a security requirement that is not bearerAuth: %v",
				strings.ToUpper(tc.method), tc.path, security)
		}

		responses := getMap(op, "responses")
		for _, want := range []string{"401", "403"} {
			if getMap(responses, want) == nil {
				t.Errorf("[P0] %s %s does not document %s. A coordinator or student reaching this endpoint is a "+
					"403 the portal branches on (AC 11), and an undocumented status is an untyped branch in the "+
					"generated client", strings.ToUpper(tc.method), tc.path, want)
			}
		}
	}

	// Without this the whole loop is skippable: with neither operation declared,
	// every iteration `continue`s after one error and a later refactor that
	// downgrades those to warnings would leave a test asserting properties of
	// operations it never found.
	if inspected != 2 {
		t.Errorf("[P0] inspected %d authenticated dispute operations, want 2 (list + resolution)", inspected)
	}
}

// 0.11-CONTRACT-007 — the resolution operation's own shape (AC 12, AC 13).
//
// The three refusals are three DIFFERENT answers and each needs a typed status:
// already-resolved is a 409, unknown is a 404, out-of-branch is a 403. Collapsing
// any pair is what produces "the resolve button did nothing" in the portal.
func TestDisputeContract_ResolutionOperationShape(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	op := getMap(getMap(getMap(doc, "paths"), disputeResolutionPath), "post")
	if op == nil {
		t.Fatalf("[P0] missing authenticated operation POST %s", disputeResolutionPath)
	}

	disputeID := indexParams(op)["dispute_id"]
	if disputeID == nil {
		t.Fatalf("[P0] POST %s has no `dispute_id` parameter", disputeResolutionPath)
	}
	if got := getStr(disputeID, "in"); got != "path" {
		t.Errorf("[P0] dispute_id.in = %q, want path", got)
	}
	if got := getStr(resolveSchema(doc, getMap(disputeID, "schema")), "format"); got != "uuid" {
		t.Errorf("[P0] dispute_id.format = %q, want uuid — a malformed id is a typed 400 at the edge, never a query", got)
	}

	body := getMap(getMap(getMap(getMap(op, "requestBody"), "content"), "application/json"), "schema")
	if got := getStr(body, "$ref"); got != "#/components/schemas/DisputeResolutionRequest" {
		t.Errorf("[P0] POST %s request body $ref = %q, want #/components/schemas/DisputeResolutionRequest",
			disputeResolutionPath, got)
	}

	note := resolveSchema(doc, getMap(getMap(resolveSchema(doc, body), "properties"), "resolution_note"))
	if note == nil {
		t.Errorf("[P0] DisputeResolutionRequest has no resolution_note property; the note IS the resolution (T15)")
	} else {
		if got := getFloat(note, "minLength"); got < 1 {
			t.Errorf("[P0] resolution_note.minLength = %v, want at least 1. An empty note closes a dispute with "+
				"no record of WHY, which is the one thing a resolution is for (AC 12)", got)
		}
		if got := getFloat(note, "maxLength"); got != 2000 {
			t.Errorf("[P0] resolution_note.maxLength = %v, want 2000 (the column's CHECK)", got)
		}
	}

	assertResponseFamily(t, doc, op, map[string]string{
		"200": "#/components/schemas/Dispute",
		"400": "#/components/schemas/ValidationError",
		"401": "#/components/schemas/Error",
		"403": "#/components/schemas/Error",
		"404": "#/components/schemas/Error",
		"409": "#/components/schemas/Error",
		"500": "#/components/schemas/Error",
	})
}

// 0.11-CONTRACT-008 — the four new error codes exist in the ErrorCode enum
// (AC 19).
//
// The enum is the SSOT and domain.codeToStatus's keys are the GENERATED enum, so
// adding a code here without adding it to errors.go fails to COMPILE. This test
// is the other direction: a code invented in Go that never reached the contract.
func TestDisputeContract_NewErrorCodesAreDeclared(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schema := getMap(getMap(getMap(doc, "components"), "schemas"), "Error")
	if schema == nil {
		t.Fatalf("[P0] components.schemas.Error is missing")
	}
	enum, _ := getMap(getMap(schema, "properties"), "code")["enum"].([]any)
	declared := map[string]bool{}
	for _, item := range enum {
		if value, ok := item.(string); ok {
			declared[value] = true
		}
	}

	for code, why := range map[string]string{
		"DISPUTE_IDENTITY_UNVERIFIED": "the ONE non-disclosing refusal shared by a bad domain, an unknown address " +
			"and an email/selection mismatch (T3/T5) — one code, because three codes is a directory oracle",
		"DISPUTE_ALREADY_OPEN":     "the 409 mapped from a 23505 on uq_disputes_open_student_event_date_slot (T11 as amended)",
		"DISPUTE_NOT_FOUND":        "an unknown dispute id on the resolution path (AC 13)",
		"DISPUTE_ALREADY_RESOLVED": "a second resolution attempt, which must write nothing (AC 13)",
	} {
		if !declared[code] {
			t.Errorf("[P0] ErrorCode enum is missing %s — %s. Add it here, to codeToStatus in "+
				"api/internal/domain/errors.go, and regenerate: the map's keys ARE this enum, so a missing "+
				"entry is a compile failure rather than a runtime surprise", code, why)
		}
	}
}

// requiredSet reads a schema's `required` list into a set.
func requiredSet(schema map[string]any) map[string]bool {
	raw, _ := schema["required"].([]any)
	out := make(map[string]bool, len(raw))
	for _, item := range raw {
		if value, ok := item.(string); ok {
			out[value] = true
		}
	}
	return out
}

// A CLIENT MUST NOT BE ABLE TO CLAIM `capture_method: dispute`.
//
// `dispute` says a teacher approved a missed-slot claim, and it is now the
// marker distinguishing a record the server wrote on their behalf from one
// somebody searched for. If the marking request accepted it, any coordinator
// could post a record that claims an approval that never happened — and the
// audit trail would agree with them. The value belongs to CaptureMethod (what a
// record may BE) and must stay out of MarkAttendanceRequest (what a client may
// SEND).
func TestDisputeContract_MarkingRequestCannotClaimADisputeCapture(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	record := enumStrings(resolveSchema(doc, getMap(schemas, "CaptureMethod")))
	if !slices.Contains(record, "dispute") {
		t.Errorf("[P0] CaptureMethod = %v, want dispute included — a record created by an approved dispute has "+
			"to be distinguishable from a search mark", record)
	}

	request := getMap(schemas, "MarkAttendanceRequest")
	if request == nil {
		t.Fatal("[P0] components.schemas.MarkAttendanceRequest is missing")
	}
	sent := enumStrings(resolveSchema(doc, getMap(getMap(request, "properties"), "capture_method")))
	if len(sent) == 0 {
		t.Fatal("[P0] MarkAttendanceRequest.capture_method declares no enum, so it accepts any string — " +
			"including dispute")
	}
	if slices.Contains(sent, "dispute") {
		t.Errorf("[P0] MarkAttendanceRequest.capture_method = %v and admits `dispute`. A client that can send it "+
			"can forge a record claiming a teacher approved a dispute, and the audit row would agree", sent)
	}
	// The request's enum must otherwise stay a SUBSET of what a record may be —
	// a client value the record schema cannot express would be unrenderable.
	for _, value := range sent {
		if !slices.Contains(record, value) {
			t.Errorf("[P0] MarkAttendanceRequest.capture_method admits %q, which CaptureMethod does not", value)
		}
	}
}

// The resolution request must carry an outcome, and it must be required.
//
// Optional-with-a-default is the failure mode worth naming: whichever way the
// default fell, it would decide "credit this student or not" without the teacher
// saying so.
func TestDisputeContract_ResolutionRequiresAnExplicitOutcome(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	request := getMap(schemas, "DisputeResolutionRequest")
	if request == nil {
		t.Fatal("[P0] components.schemas.DisputeResolutionRequest is missing")
	}
	if missing := missingRequired(request, "resolution_note", "outcome"); len(missing) > 0 {
		t.Errorf("[P0] DisputeResolutionRequest required is missing %v", missing)
	}
	outcome := getMap(getMap(request, "properties"), "outcome")
	if got := enumStrings(resolveSchema(doc, outcome)); !slices.Equal(got, []string{"approved", "rejected"}) {
		t.Errorf("[P0] outcome enum = %v, want [approved rejected]", got)
	}
	if _, hasDefault := outcome["default"]; hasDefault {
		t.Errorf("[P0] outcome declares a default. A default decides whether a student's slot is credited " +
			"without the teacher choosing, in whichever direction it happens to fall")
	}

	// And the resolved dispute reports the outcome back, as a REQUIRED key —
	// nullable while open, but never absent, or a client cannot tell "still open"
	// from "this server is too old to report one".
	resolved := getMap(schemas, "Dispute")
	if missing := missingRequired(resolved, "outcome"); len(missing) > 0 {
		t.Errorf("[P0] Dispute required is missing %v", missing)
	}
}
