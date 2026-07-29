package contract

import (
	"slices"
	"testing"
)

const (
	cardSealPath    = "/api/v1/admin/students/{student_id}/card-seal"
	cardResolvePath = "/api/v1/admin/card-tokens/resolve"
)

func TestCardTokenContract_AdminSealOperation(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	path := getMap(getMap(doc, "paths"), cardSealPath)
	if path == nil {
		t.Fatalf("[P0] missing card seal path %s", cardSealPath)
	}
	post := getMap(path, "post")
	if post == nil {
		t.Fatalf("[P0] missing POST %s", cardSealPath)
	}
	if got := getStr(post, "operationId"); got != "sealAdminStudentCard" {
		t.Errorf("[P1] operationId = %q, want sealAdminStudentCard", got)
	}
	assertBearerSecurity(t, post, "POST "+cardSealPath)
	if ref := getStr(jsonResponseSchema(doc, post, "201"), "$ref"); ref != "#/components/schemas/CardSeal" {
		t.Errorf("[P0] 201 schema = %q, want CardSeal", ref)
	}
	assertResponseFamily(t, doc, post, map[string]string{
		"400": "#/components/schemas/ValidationError",
		"401": "#/components/schemas/Error",
		"403": "#/components/schemas/Error",
		"404": "#/components/schemas/Error",
		"500": "#/components/schemas/Error",
	})
}

func TestCardTokenContract_AdminResolveOperation(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	path := getMap(getMap(doc, "paths"), cardResolvePath)
	if path == nil {
		t.Fatalf("[P0] missing card token resolve path %s", cardResolvePath)
	}
	post := getMap(path, "post")
	if post == nil {
		t.Fatalf("[P0] missing POST %s", cardResolvePath)
	}
	if got := getStr(post, "operationId"); got != "resolveAdminCardToken" {
		t.Errorf("[P1] operationId = %q, want resolveAdminCardToken", got)
	}
	assertBearerSecurity(t, post, "POST "+cardResolvePath)
	if ref := getStr(jsonRequestSchema(post), "$ref"); ref != "#/components/schemas/CardTokenResolveRequest" {
		t.Errorf("[P0] request schema = %q, want CardTokenResolveRequest", ref)
	}
	if ref := getStr(jsonResponseSchema(doc, post, "200"), "$ref"); ref != "#/components/schemas/ResolvedCardStudent" {
		t.Errorf("[P0] 200 schema = %q, want ResolvedCardStudent", ref)
	}
	assertResponseFamily(t, doc, post, map[string]string{
		"400": "#/components/schemas/ValidationError",
		"401": "#/components/schemas/Error",
		"403": "#/components/schemas/Error",
		"422": "#/components/schemas/Error",
		"500": "#/components/schemas/Error",
	})
}

func TestCardTokenContract_SchemasProtectTheSecretBoundary(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	schemas := getMap(getMap(doc, "components"), "schemas")

	seal := getMap(schemas, "CardSeal")
	if seal == nil {
		t.Fatal("[P0] components.schemas.CardSeal is missing")
	}
	sealProps := getMap(seal, "properties")
	for _, field := range []string{"student", "version", "token", "payload"} {
		if _, ok := sealProps[field]; !ok {
			t.Errorf("[P0] CardSeal missing %s", field)
		}
	}
	if missing := missingRequired(seal, "student", "version", "token", "payload"); len(missing) > 0 {
		t.Errorf("[P0] CardSeal required is missing %v", missing)
	}
	payloadProps := schemaProps(doc, getMap(sealProps, "payload"))
	for _, field := range []string{"version", "token"} {
		if _, ok := payloadProps[field]; !ok {
			t.Errorf("[P0] CardSeal.payload missing %s", field)
		}
	}
	for _, forbidden := range []string{"roll_no", "name", "student_id", "branch", "section", "batch", "email", "key", "hmac_key"} {
		if _, ok := payloadProps[forbidden]; ok {
			t.Errorf("[P0] CardSeal.payload must not expose %s", forbidden)
		}
	}
	if got, ok := getMap(sealProps, "payload")["additionalProperties"].(bool); !ok || got {
		t.Error("[P0] CardSeal.payload must set additionalProperties: false")
	}
	if token := getMap(sealProps, "token"); getStr(token, "type") != "string" {
		t.Error("[P0] CardSeal.token must be a string")
	}

	resolveReq := getMap(schemas, "CardTokenResolveRequest")
	if resolveReq == nil {
		t.Fatal("[P0] components.schemas.CardTokenResolveRequest is missing")
	}
	if missing := missingRequired(resolveReq, "version", "token"); len(missing) > 0 {
		t.Errorf("[P0] CardTokenResolveRequest required is missing %v", missing)
	}

	student := getMap(schemas, "ResolvedCardStudent")
	if student == nil {
		t.Fatal("[P0] components.schemas.ResolvedCardStudent is missing")
	}
	studentProps := getMap(student, "properties")
	for _, field := range []string{"student_id", "roll_no", "display_name", "branch", "section", "batch"} {
		if _, ok := studentProps[field]; !ok {
			t.Errorf("[P0] ResolvedCardStudent missing %s", field)
		}
	}
	for _, forbidden := range []string{"token", "hmac_key", "key"} {
		if _, ok := studentProps[forbidden]; ok {
			t.Errorf("[P0] ResolvedCardStudent must not expose %s", forbidden)
		}
	}
}

func TestCardTokenContract_ErrorCodesIncludeCardResolutionFailures(t *testing.T) {
	doc := loadSpec(t, repoRoot(t))
	errorSchema := getMap(getMap(getMap(doc, "components"), "schemas"), "Error")
	code := getMap(getMap(errorSchema, "properties"), "code")
	missing := containsAll(enumStrings(code), []string{"CARD_NOT_RECOGNIZED", "STUDENT_NOT_FOUND"})
	if len(missing) > 0 {
		t.Errorf("[P0] Error.code enum missing %v", missing)
	}
}

func assertBearerSecurity(t *testing.T, op map[string]any, label string) {
	t.Helper()
	security, ok := op["security"].([]any)
	if !ok || len(security) == 0 {
		t.Fatalf("[P0] %s must require bearer authentication", label)
	}
}

func assertResponseFamily(t *testing.T, doc, op map[string]any, want map[string]string) {
	t.Helper()
	for status, ref := range want {
		if got := getStr(jsonResponseSchema(doc, op, status), "$ref"); got != ref {
			t.Errorf("[P0] status %s schema = %q, want %s", status, got, ref)
		}
	}
}

func missingRequired(schema map[string]any, want ...string) []string {
	raw, _ := schema["required"].([]any)
	got := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			got = append(got, s)
		}
	}
	var missing []string
	for _, field := range want {
		if !slices.Contains(got, field) {
			missing = append(missing, field)
		}
	}
	return missing
}
