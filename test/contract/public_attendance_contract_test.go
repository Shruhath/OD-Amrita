// Contract gates for Story 0.10's unauthenticated public attendance surface.
//
// RED PHASE. These parse contracts/openapi.yaml as YAML — no generated Go types —
// so this file COMPILES today and fails on assertions with a message that names
// the missing thing. That is deliberate: a package that fails to build reports
// one opaque error for the whole suite, and the contract is the first artifact
// the dev touches.
//
// DELIBERATELY NOT GATED behind an env flag. Story 0.9 shipped an entire suite
// behind ATDD_ACTIVE_09, which nothing ever set, so it never ran at all. These
// go red in normal `go test ./...` and go green when the contract is written.
package contract

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

const (
	publicSearchPath  = "/api/v1/public/students/search"
	publicRecordsPath = "/api/v1/public/students/{student_id}/od-records"
)

// publicSchemaAllowlists is the load-bearing table of this file.
//
// It is an ALLOWLIST, not a blocklist. Story 0.9's equivalent test listed the
// sensitive names it did not want (email, token, ...) and asserted their
// absence — which could never fail, because a schema that never declared them
// passes trivially, and the NEXT field someone adds is not on the list either.
// An exact property-set match fails the moment any field appears that was not
// argued for here, which is the only form of this test that protects an
// open-internet response (AC 5).
var publicSchemaAllowlists = map[string][]string{
	"PublicStudent": {"student_id", "roll_no", "display_name", "branch", "section", "batch"},
	// `od_date` joins both shapes under Story 0.12a, and the two dates are NOT
	// redundant. On the RECORD, `event_date` stays the event's FIRST day because
	// eventsFromRecords() builds the dispute form's event options from it, while
	// `od_date` is the day this particular OD belongs to. On the DAY group,
	// `event_date` is RENAMED to `od_date` rather than supplemented: the grouping
	// key must not lie about which fact it holds, or a thirty-day festival
	// collapses onto one heading and reads as twenty-nine days of missing
	// attendance.
	//
	// Both are disclosure decisions like every other entry here — a date is the
	// one field on this response that says when a named student was physically
	// somewhere, and it is served to the open internet.
	"PublicODRecord":             {"record_id", "slot", "event_id", "event_name", "event_date", "od_date", "category_id", "category_name", "marked_by_name", "marked_by_role", "marked_at"},
	"PublicODDay":                {"od_date", "records"},
	"PublicODRecordCollection":   {"student", "days", "generated_at"},
	"PublicStudentSearchResults": {"data"},
}

// 0.10-CONTRACT-001 — the whole story in one assertion: neither public operation
// declares `security`.
//
// The spec declares no root-level `security`, so under OpenAPI 3.0.3 the ABSENCE
// of the key is what makes an operation unauthenticated. Every other operation in
// the file carries `security: [bearerAuth: []]`; copying one as a starting point
// and forgetting to delete that block produces a spec that documents a 401 on a
// page no student can sign in to.
func TestPublicAttendanceContract_BothOperationsAreUnauthenticated(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))

	if _, declared := doc["security"]; declared {
		t.Fatalf("[P0] the spec now declares a ROOT-level `security`; omitting `security` on an operation " +
			"no longer means unauthenticated, and the public operations would inherit the global requirement")
	}

	paths := getMap(doc, "paths")
	for _, tc := range []struct{ path, operationID string }{
		{publicSearchPath, "searchPublicStudents"},
		{publicRecordsPath, "listPublicStudentODRecords"},
	} {
		op := getMap(getMap(paths, tc.path), "get")
		if op == nil {
			t.Errorf("[P0] missing public path GET %s — a student cannot read their OD credit without it", tc.path)
			continue
		}
		if _, declared := op["security"]; declared {
			t.Errorf("[P0] GET %s declares `security` — every visitor is anonymous, so this endpoint would 401 "+
				"for its entire audience. Omit the key; do not set `security: []`, which reads as a deliberate "+
				"override of a global requirement that does not exist here", tc.path)
		}
		if got := getStr(op, "operationId"); got != tc.operationID {
			t.Errorf("[P0] GET %s operationId = %q, want %q", tc.path, got, tc.operationID)
		}
		// No 401 and no 403 may be documented: there is no credential to be
		// missing and no role to be insufficient. A documented 401 is a
		// generated client that handles a sign-out path on a public page.
		for _, forbidden := range []string{"401", "403"} {
			if getMap(getMap(op, "responses"), forbidden) != nil {
				t.Errorf("[P0] GET %s documents a %s response; a public endpoint has no authentication to fail",
					tc.path, forbidden)
			}
		}
		// 429 is the ONLY refusal a public caller can provoke, so it must be
		// documented on both or the generated client has no typed branch for it.
		if getMap(getMap(op, "responses"), "429") == nil {
			t.Errorf("[P0] GET %s does not document 429; RATE_LIMITED is the one refusal this surface can return",
				tc.path)
		}
	}
}

// 0.10-CONTRACT-002 — the search accepts a bounded `q` and NO caller-supplied
// result cap.
//
// T7: a knob the open internet controls is a knob that gets turned to 10,000.
// The authenticated /api/v1/students/search has a `limit` parameter; this one
// must not, and the cap is fixed server-side at 10.
func TestPublicAttendanceContract_SearchBoundsAndNoLimitKnob(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	op := getMap(getMap(getMap(doc, "paths"), publicSearchPath), "get")
	if op == nil {
		t.Fatalf("[P0] missing public path GET %s", publicSearchPath)
	}

	params := indexParams(op)

	q := params["q"]
	if q == nil {
		t.Fatalf("[P0] GET %s has no `q` parameter", publicSearchPath)
	}
	if required, _ := q["required"].(bool); !required {
		t.Errorf("[P0] `q` must be required — an absent query is a roster dump request")
	}
	schema := resolveSchema(doc, getMap(q, "schema"))
	if got := getFloat(schema, "minLength"); got != 3 {
		t.Errorf("[P0] q.minLength = %v, want 3 (svcstudent.MinSearchRunes — one number, not a second copy)", got)
	}
	if got := getFloat(schema, "maxLength"); got != 64 {
		t.Errorf("[P0] q.maxLength = %v, want 64 (svcstudent.MaxSearchRunes)", got)
	}

	if params["limit"] != nil {
		t.Errorf("[P0] GET %s accepts a `limit` parameter; the public cap is fixed server-side at 10 and "+
			"must not be callable-supplied (T7)", publicSearchPath)
	}
	for name := range params {
		if name != "q" {
			t.Errorf("[P1] GET %s accepts unexpected parameter %q; every accepted input on an open endpoint "+
				"is a bound someone has to defend", publicSearchPath, name)
		}
	}

	assertResponseFamily(t, doc, op, map[string]string{
		"200": "#/components/schemas/PublicStudentSearchResults",
		"400": "#/components/schemas/ValidationError",
		"429": "#/components/schemas/Error",
		"500": "#/components/schemas/Error",
	})
}

// 0.10-CONTRACT-003 — the records read is keyed by a uuid path parameter and
// carries the typed 404/400 family the UI branches on (AC 6, AC 12).
func TestPublicAttendanceContract_RecordsOperationShape(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	op := getMap(getMap(getMap(doc, "paths"), publicRecordsPath), "get")
	if op == nil {
		t.Fatalf("[P0] missing public path GET %s", publicRecordsPath)
	}

	params := indexParams(op)
	studentID := params["student_id"]
	if studentID == nil {
		t.Fatalf("[P0] GET %s has no `student_id` parameter", publicRecordsPath)
	}
	if got := getStr(studentID, "in"); got != "path" {
		t.Errorf("[P0] student_id.in = %q, want path", got)
	}
	if got := getStr(resolveSchema(doc, getMap(studentID, "schema")), "format"); got != "uuid" {
		t.Errorf("[P0] student_id.format = %q, want uuid — a malformed id must be a typed 400 at the edge, "+
			"never a query", got)
	}

	assertResponseFamily(t, doc, op, map[string]string{
		"200": "#/components/schemas/PublicODRecordCollection",
		"400": "#/components/schemas/ValidationError",
		"404": "#/components/schemas/Error",
		"429": "#/components/schemas/Error",
		"500": "#/components/schemas/Error",
	})
}

// 0.10-CONTRACT-004 — each public schema's property set matches its allowlist
// EXACTLY (AC 5).
func TestPublicAttendanceContract_SchemaAllowlists(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	for _, name := range sortedKeys(publicSchemaAllowlists) {
		allowed := publicSchemaAllowlists[name]
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
				t.Errorf("[P0] %s exposes %q, which is not on the public allowlist. This schema is served to the "+
					"OPEN INTERNET with no authentication: adding a field here is a disclosure decision, so add it "+
					"to publicSchemaAllowlists in the same change and say why", name, field)
			}
		}
		if missing := missingRequired(schema, allowed...); len(missing) > 0 {
			t.Errorf("[P1] %s required is missing %v; an optional key in a generated client becomes an "+
				"`| undefined` the UI silently renders as blank", name, missing)
		}
	}

	// The marker is a NAME and a ROLE. Not a user_id, not an email, not a
	// contact detail — AttendanceActor carries an email and must never be the
	// shape this reuses (T4).
	record := getMap(schemas, "PublicODRecord")
	if record != nil {
		props := getMap(record, "properties")
		if nullable, _ := getMap(props, "marked_by_name")["nullable"].(bool); !nullable {
			t.Errorf("[P0] PublicODRecord.marked_by_name must be nullable — actor_user_id is ON DELETE SET NULL, " +
				"so a deleted staff row must render the record, not break the page")
		}
	}
}

// 0.10-CONTRACT-005 — no public schema reaches an authenticated schema by $ref.
//
// T3: their fields overlap TODAY. Sharing them means a field added tomorrow for
// the admin surface — a credit balance, an email, a dispute note — ships to the
// open internet with nobody reviewing that decision. The whole subtree is walked,
// so a $ref nested three levels down inside an array item is caught too.
func TestPublicAttendanceContract_PublicSchemasNeverRefAnAuthenticatedSchema(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	forbidden := []string{"AttendanceRecord", "AttendanceActor", "AttendanceRecordCollection", "ResolvedCardStudent", "StudentSearchResults", "CardSeal", "AuthenticatedIdentity"}

	for _, name := range sortedKeys(publicSchemaAllowlists) {
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
			needle := fmt.Sprintf("#/components/schemas/%s", target)
			// Guard against a prefix match: AttendanceRecord is a prefix of
			// AttendanceRecordCollection, and a substring hit on the shorter
			// name would report the wrong schema.
			if strings.Contains(body, needle+`"`) {
				t.Errorf("[P0] %s $refs the authenticated schema %s. Public schemas are DISTINCT types (T3) — "+
					"a shared schema means the next field added for the admin surface is published without review",
					name, target)
			}
		}
	}
}

// deliberatelyUnauthenticated is the complete, argued-for set of operations that
// may omit `security`. Everything else in the spec must require a bearer token.
//
// /health is the pre-existing precedent: it is mounted on the BARE router in
// api/cmd/api/main.go, outside the /api/v1 group, and answers a liveness probe.
// The two public reads are what this story adds. There is no fourth entry, and
// adding one is a disclosure decision that belongs in this list with a reason.
// Keyed by "METHOD path", not by path alone. A path-keyed exemption is inherited
// by every operation later added under it, so a POST dropped onto either public
// path would be waved past the bearer requirement by the very table that exists
// to enumerate what may skip it — while the package doc insists no write may
// exist in this group at all.
// Story 0.11 adds the two entries below, and they are the first WRITE and the
// first non-student-scoped read on this list. Both are argued for in the story's
// locked decisions: the write is identity-verified against the roster (T1/T2),
// rate limited by a second stricter bucket of its own (T14), duplicate-guarded by
// a database constraint (T11) and audited under the submitter's own email (T13);
// the event read returns four fields per event and no branch, coordinator, count
// or archive metadata (T7). A third entry is a disclosure decision that belongs
// here with a reason, exactly like these.
var deliberatelyUnauthenticated = map[string]string{
	"get /health":              "liveness probe on the bare router, outside /api/v1",
	"get " + publicSearchPath:  "public dashboard search (FR38)",
	"get " + publicRecordsPath: "public dashboard records read (FR39)",
	"post " + publicDisputePath: "student dispute submission — the ONLY unauthenticated write in the system " +
		"(FR41-FR43); identity-verified, audited, and behind its own stricter limiter",
	"get " + publicEventsPath: "the four-field event list a never-marked student needs to name what they " +
		"are disputing (T7); no branches, coordinators, counts or archive metadata",
}

// 0.10-CONTRACT-006 — the regression this story makes possible: every operation
// that is NOT deliberately unauthenticated still requires a bearer token.
//
// This story is what makes `security`-omission LOAD-BEARING. Until now, an
// operation missing the block was merely untidy — nothing read the absence as a
// statement. From 0.10 onward, omission IS the machine-readable declaration that
// an endpoint is open to the internet, so every existing omission has to be
// either fixed or argued for. Adding an unauthenticated group is also exactly
// the change during which somebody deletes a `security` block from the wrong
// operation, and nothing else in the contract suite would notice.
func TestPublicAttendanceContract_EveryOtherOperationStillRequiresBearer(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	paths := getMap(doc, "paths")

	methods := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}

	for path, raw := range paths {
		item := asMap(raw)
		for method, rawOp := range item {
			if !methods[strings.ToLower(method)] {
				continue
			}
			if _, exempt := deliberatelyUnauthenticated[strings.ToLower(method)+" "+path]; exempt {
				continue
			}
			op := asMap(rawOp)
			security, ok := op["security"].([]any)
			if ok && len(security) > 0 {
				continue
			}
			// An operation that documents a 401 while declaring no security is
			// self-contradictory, and that contradiction is the evidence this is
			// an oversight rather than an intended public endpoint.
			hint := ""
			if getMap(getMap(op, "responses"), "401") != nil {
				hint = " It documents a 401 response while declaring no way to authenticate, so this is an " +
					"omission, not an intent: add `security: [- bearerAuth: []]` to it."
			}
			t.Errorf("[P0] %s %s does not require bearer authentication. From Story 0.10 onward, omitting "+
				"`security` is how this spec DECLARES an endpoint public, so this operation now reads as open "+
				"to the internet.%s If it really is public, add it to deliberatelyUnauthenticated with a reason.",
				strings.ToUpper(method), path, hint)
		}
	}
}

// indexParams collects an operation's parameters by name.
func indexParams(op map[string]any) map[string]map[string]any {
	raw, _ := op["parameters"].([]any)
	out := make(map[string]map[string]any, len(raw))
	for _, item := range raw {
		p := asMap(item)
		out[getStr(p, "name")] = p
	}
	return out
}

// sortedKeys keeps the failure output stable so a diff between two red runs is
// readable rather than reshuffled by map iteration order.
func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
