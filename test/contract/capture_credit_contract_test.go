package contract

import (
	"slices"
	"strings"
	"testing"
)

const (
	captureCreditSummaryPath = "/api/v1/admin/events/{event_id}/capture-credits"
	captureCreditLimitPath   = "/api/v1/admin/events/{event_id}/capture-credits/coordinators/{user_id}/limit"
	captureCreditResetPath   = "/api/v1/admin/events/{event_id}/capture-credits/coordinators/{user_id}/reset"
)

func TestCaptureCreditContract_AdminOperationsAreNestedAndProtected(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	paths := getMap(doc, "paths")

	cases := []struct {
		path        string
		method      string
		operationID string
		response    string
	}{
		{
			path:        captureCreditSummaryPath,
			method:      "get",
			operationID: "listAdminEventCaptureCredits",
			response:    "#/components/schemas/CaptureCreditSummary",
		},
		{
			path:        captureCreditLimitPath,
			method:      "put",
			operationID: "updateAdminEventCoordinatorCaptureCreditLimit",
			response:    "#/components/schemas/CaptureCreditUsage",
		},
		{
			path:        captureCreditResetPath,
			method:      "post",
			operationID: "resetAdminEventCoordinatorCaptureCredits",
			response:    "#/components/schemas/CaptureCreditUsage",
		},
	}

	for _, tc := range cases {
		item := getMap(paths, tc.path)
		if item == nil {
			t.Fatalf("[P0] missing capture-credit admin path %s", tc.path)
		}
		op := getMap(item, tc.method)
		if op == nil {
			t.Fatalf("[P0] missing %s %s", tc.method, tc.path)
		}
		if got := getStr(op, "operationId"); got != tc.operationID {
			t.Errorf("[P1] %s %s operationId = %q, want %s", tc.method, tc.path, got, tc.operationID)
		}
		assertBearerSecurity(t, op, tc.method+" "+tc.path)
		if ref := getStr(jsonResponseSchema(doc, op, "200"), "$ref"); ref != tc.response {
			t.Errorf("[P0] %s %s 200 schema = %q, want %s", tc.method, tc.path, ref, tc.response)
		}
		assertResponseFamily(t, doc, op, map[string]string{
			"400": "#/components/schemas/ValidationError",
			"401": "#/components/schemas/Error",
			"403": "#/components/schemas/Error",
			"404": "#/components/schemas/Error",
			"409": "#/components/schemas/Error",
			"500": "#/components/schemas/Error",
		})
	}

	if _, ok := paths["/api/v1/admin/events/{event_id}/capture-credits/config"]; ok {
		t.Fatal("[P0] global capture-credit config path is still documented; caps must be coordinator-specific")
	}
	if ref := getStr(jsonRequestSchema(getMap(getMap(paths, captureCreditLimitPath), "put")), "$ref"); ref != "#/components/schemas/UpdateCaptureCreditLimitRequest" {
		t.Errorf("[P0] PUT %s request schema = %q, want UpdateCaptureCreditLimitRequest", captureCreditLimitPath, ref)
	}
}

func TestCaptureCreditContract_MarkStatusCarriesRequiredFallbackCreditState(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")
	markStatus := getMap(schemas, "MarkStatus")
	if markStatus == nil {
		t.Fatal("[P0] components.schemas.MarkStatus is missing")
	}
	if missing := missingRequired(markStatus, "server_time", "slot", "counters", "fallback_credit"); len(missing) > 0 {
		t.Errorf("[P0] MarkStatus required is missing %v; fallback_credit must be required so old servers fail loudly", missing)
	}

	props := getMap(markStatus, "properties")
	creditRef := getStr(getMap(props, "fallback_credit"), "$ref")
	if creditRef != "#/components/schemas/FallbackCreditState" {
		t.Fatalf("[P0] MarkStatus.fallback_credit schema = %q, want FallbackCreditState", creditRef)
	}

	state := getMap(schemas, "FallbackCreditState")
	if state == nil {
		t.Fatal("[P0] components.schemas.FallbackCreditState is missing")
	}
	if missing := missingRequired(state, "limit", "consumed", "remaining", "exhausted"); len(missing) > 0 {
		t.Errorf("[P0] FallbackCreditState required is missing %v", missing)
	}
	stateProps := getMap(state, "properties")
	for _, name := range []string{"limit", "consumed", "remaining"} {
		field := resolveSchema(doc, getMap(stateProps, name))
		if got := getStr(field, "type"); got != "integer" {
			t.Errorf("[P0] FallbackCreditState.%s type = %q, want integer", name, got)
		}
		if got := getFloat(field, "minimum"); got != 0 {
			t.Errorf("[P0] FallbackCreditState.%s minimum = %v, want 0", name, got)
		}
	}
	if got := getStr(resolveSchema(doc, getMap(stateProps, "exhausted")), "type"); got != "boolean" {
		t.Errorf("[P0] FallbackCreditState.exhausted type = %q, want boolean", got)
	}
}

func TestCaptureCreditContract_AdminSchemasAreEventScopedAndBounded(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	for _, name := range []string{
		"CaptureCreditUsage",
		"CaptureCreditSummary",
		"UpdateCaptureCreditLimitRequest",
	} {
		if getMap(schemas, name) == nil {
			t.Errorf("[P0] components.schemas.%s is missing", name)
		}
	}

	if getMap(schemas, "CaptureCreditConfig") != nil {
		t.Error("[P0] CaptureCreditConfig still exists; CaptureCreditSummary must not represent one unified mutable cap")
	}

	usage := getMap(schemas, "CaptureCreditUsage")
	if usage != nil {
		if missing := missingRequired(usage, "event_id", "user_id", "email", "name", "limit", "consumed", "remaining", "exhausted"); len(missing) > 0 {
			t.Errorf("[P0] CaptureCreditUsage required is missing %v", missing)
		}
		props := getMap(usage, "properties")
		for _, forbidden := range []string{"category_id", "slot", "card", "token", "token_fingerprint"} {
			if _, ok := props[forbidden]; ok {
				t.Errorf("[P0] CaptureCreditUsage must not expose %s", forbidden)
			}
		}
		limit := resolveSchema(doc, getMap(props, "limit"))
		if got := getFloat(limit, "minimum"); got != 0 {
			t.Errorf("[P0] CaptureCreditUsage.limit minimum = %v, want 0", got)
		}
		if got := getFloat(limit, "maximum"); got != 2147483647 {
			t.Errorf("[P0] CaptureCreditUsage.limit maximum = %v, want int32 max", got)
		}
		if desc := getStr(limit, "description"); !strings.Contains(strings.ToLower(desc), "coordinator") {
			t.Errorf("[P0] CaptureCreditUsage.limit description = %q, want coordinator-specific documentation", desc)
		}
	}

	summary := getMap(schemas, "CaptureCreditSummary")
	if summary != nil {
		if missing := missingRequired(summary, "data"); len(missing) > 0 {
			t.Errorf("[P0] CaptureCreditSummary required is missing %v", missing)
		}
		props := getMap(summary, "properties")
		if _, ok := props["config"]; ok {
			t.Error("[P0] CaptureCreditSummary.config still exists; there is no unified mutable cap")
		}
		for _, forbidden := range []string{"page", "page_size", "total"} {
			if _, ok := props[forbidden]; ok {
				t.Errorf("[P1] CaptureCreditSummary must not be paginated with %s; coordinator assignments are bounded event setup rows", forbidden)
			}
		}
	}

	update := getMap(schemas, "UpdateCaptureCreditLimitRequest")
	if update != nil {
		if missing := missingRequired(update, "limit"); len(missing) > 0 {
			t.Errorf("[P0] UpdateCaptureCreditLimitRequest required is missing %v", missing)
		}
		if got := getFloat(resolveSchema(doc, getMap(getMap(update, "properties"), "limit")), "minimum"); got != 0 {
			t.Errorf("[P0] UpdateCaptureCreditLimitRequest.limit minimum = %v, want 0", got)
		}
		if got := getFloat(resolveSchema(doc, getMap(getMap(update, "properties"), "limit")), "maximum"); got != 2147483647 {
			t.Errorf("[P0] UpdateCaptureCreditLimitRequest.limit maximum = %v, want int32 max to prevent server-side cast overflow", got)
		}
	}
}

func TestCaptureCreditContract_LimitErrorAndAuditVocabularyAreCombinedCreditSpecific(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")
	code := getMap(getMap(getMap(schemas, "Error"), "properties"), "code")
	codes := enumStrings(code)
	if !slices.Contains(codes, "CAPTURE_CREDIT_LIMIT_REACHED") {
		t.Errorf("[P0] Error.code enum must contain CAPTURE_CREDIT_LIMIT_REACHED, got %v", codes)
	}

	mark := getMap(getMap(getMap(doc, "paths"), "/api/v1/mark"), "post")
	if ref := getStr(jsonResponseSchema(doc, mark, "429"), "$ref"); ref != "#/components/schemas/Error" {
		t.Errorf("[P0] POST /api/v1/mark must define 429 Error for exhausted camera/search credit, got %q", ref)
	}

	event := getMap(schemas, "Event")
	if _, ok := getMap(event, "properties")["capture_credits"]; ok {
		t.Error("[P2] Event must not embed unbounded per-coordinator credit rows")
	}
}
