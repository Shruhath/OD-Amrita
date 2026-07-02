// Package contract holds the acceptance harness for the spec-first OpenAPI
// contract, codegen & drift-gate.
//
// Each test asserts the EXPECTED end state of the contract wiring. It is the
// executable definition-of-done for: the real OpenAPI 3.0.3 spec (/health, the
// error envelope, typed validation details, the 202 batch scan contract),
// real two-language codegen (oapi-codegen + openapi-typescript, DO NOT EDIT),
// the real CI drift-gate, and EOL normalization.
//
// Test levels: Contract (semantic YAML parse of openapi.yaml) · Integration
// (real codegen + drift-gate via exec.Command + git) · Static/Config (presence
// + shape of oapi-codegen.yaml, Makefile, workflow, package.json, .gitattributes).
//
// Priority tags in test names: [P0] critical, [P1] important, [P2] guard.
//
// Run all: `cd test/contract && go mod tidy && GOWORK=off go test -v ./...`
// (GOWORK=off because this module is intentionally not in the repo go.work.)
package contract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Shared fixtures / helpers
// ---------------------------------------------------------------------------

// repoRoot returns the monorepo root. This package lives at <root>/test/contract,
// so the root is two directories up from the package working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("cannot resolve repo root: %v", err)
	}
	return root
}

func exists(parts ...string) bool {
	_, err := os.Stat(filepath.Join(parts...))
	return err == nil
}

func mustReadFile(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("cannot read %s: %v", filepath.Join(parts...), err)
	}
	return string(b)
}

func requireFile(t *testing.T, root, rel string) {
	t.Helper()
	if !exists(filepath.Join(root, rel)) {
		t.Errorf("expected file %q to exist", rel)
	}
}

// requireContains fails if the file content does not contain every needle.
func requireContains(t *testing.T, root, rel string, needles ...string) {
	t.Helper()
	content := mustReadFile(t, filepath.Join(root, rel))
	for _, n := range needles {
		if !strings.Contains(content, n) {
			t.Errorf("expected %q to contain %q", rel, n)
		}
	}
}

// runCmd runs a command in dir and returns combined output + error.
func runCmd(t *testing.T, dir, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// requireTool skips the test (environmental, not red-phase) when a required
// external binary is unavailable in this environment.
func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%q not available in this environment; skipping (environmental, not a red-phase pass)", name)
	}
}

// ---------------------------------------------------------------------------
// OpenAPI (YAML) parsing helpers — semantic assertions, not string matching.
// ---------------------------------------------------------------------------

// loadSpec parses contracts/openapi.yaml into a generic map for shape assertions.
func loadSpec(t *testing.T, root string) map[string]any {
	t.Helper()
	raw := mustReadFile(t, filepath.Join(root, "contracts", "openapi.yaml"))
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("contracts/openapi.yaml is not valid YAML: %v", err)
	}
	return doc
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func getMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	return asMap(m[key])
}

func getStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// resolveSchema follows a single-level `$ref` into components.schemas; if the
// node is already inline, it is returned as-is.
func resolveSchema(doc, node map[string]any) map[string]any {
	if node == nil {
		return nil
	}
	ref, ok := node["$ref"].(string)
	if !ok {
		return node
	}
	const prefix = "#/components/schemas/"
	name := strings.TrimPrefix(ref, prefix)
	if name == ref { // prefix not present — unsupported ref target
		return nil
	}
	schemas := getMap(getMap(doc, "components"), "schemas")
	return getMap(schemas, name)
}

// schemaProps returns the resolved `properties` map of a schema (following a $ref).
func schemaProps(doc, node map[string]any) map[string]any {
	return getMap(resolveSchema(doc, node), "properties")
}

// enumStrings returns the string members of a schema's `enum`.
func enumStrings(node map[string]any) []string {
	if node == nil {
		return nil
	}
	raw, ok := node["enum"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func containsAll(haystack, needles []string) []string {
	set := make(map[string]bool, len(haystack))
	for _, h := range haystack {
		set[h] = true
	}
	var missing []string
	for _, n := range needles {
		if !set[n] {
			missing = append(missing, n)
		}
	}
	return missing
}

// jsonSchema returns the application/json response schema for a given operation
// + status code (e.g. op=post path, status="202").
func jsonResponseSchema(doc, op map[string]any, status string) map[string]any {
	resp := getMap(getMap(op, "responses"), status)
	content := getMap(getMap(resp, "content"), "application/json")
	return getMap(content, "schema")
}

// jsonRequestSchema returns the application/json requestBody schema for an operation.
func jsonRequestSchema(op map[string]any) map[string]any {
	content := getMap(getMap(getMap(op, "requestBody"), "content"), "application/json")
	return getMap(content, "schema")
}

// findSchemaWithProps scans components.schemas for a schema whose `properties`
// include every required key; returns the schema node or nil.
func findSchemaWithProps(doc map[string]any, required ...string) map[string]any {
	schemas := getMap(getMap(doc, "components"), "schemas")
	for _, v := range schemas {
		props := getMap(asMap(v), "properties")
		if props == nil {
			continue
		}
		if len(containsAll(mapKeys(props), required)) == 0 {
			return asMap(v)
		}
	}
	return nil
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The error-code registry that must appear in the spec's Error.code enum
// [Source: architecture.md#Communication-Patterns lines 476-479].
var errorCodeRegistry = []string{
	"AUTH_DOMAIN_REJECTED", "RBAC_FORBIDDEN", "SCAN_DUPLICATE", "SLOT_CLOSED",
	"CARD_NOT_RECOGNIZED", "QR_LIMIT_REACHED", "RATE_LIMITED", "INVALID_SCAN_ID",
	"INTERNAL", "VALIDATION_FAILED",
}

// ---------------------------------------------------------------------------
// assertRealCodegen — the guard that makes the drift / build tests RED today.
// A no-op stub generator is trivially idempotent and its (placeholder) output
// builds; so the drift-gate/build scenarios only carry signal once REAL codegen
// output exists. Each such test calls this first.
// ---------------------------------------------------------------------------

func assertRealCodegen(t *testing.T, root string) {
	t.Helper()
	// Go: at least one generated .go file beyond the placeholder doc.go, carrying
	// real generated type declarations (oapi-codegen emits `type ... struct`).
	goDir := filepath.Join(root, "contracts", "generated", "go")
	entries, _ := os.ReadDir(goDir)
	foundGoTypes := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src := mustReadFile(t, filepath.Join(goDir, e.Name()))
		if strings.Contains(src, "DO NOT EDIT") && strings.Contains(src, "type ") {
			foundGoTypes = true
			break
		}
	}
	if !foundGoTypes {
		t.Errorf("no real generated Go types found under contracts/generated/go (still a placeholder?) — run real `oapi-codegen`")
	}
	// TS: a generated schema module must exist.
	if !exists(filepath.Join(root, "contracts", "generated", "ts", "schema.ts")) {
		t.Errorf("contracts/generated/ts/schema.ts missing — run real `openapi-typescript`")
	}
}

// ===========================================================================
// AC1 / AC3 — CONTRACT LEVEL: the spec is a real, valid 3.0.3 document.
// ===========================================================================

// [P0] AC1: the spec is OpenAPI 3.0.3 (NOT 3.1.0 — oapi-codegen v2 / kin-openapi
// cannot consume 3.1). This is the #1 failure-mode guardrail for the contract.
func TestAC1_SpecIsOpenAPI303(t *testing.T) {
	root := repoRoot(t)
	doc := loadSpec(t, root)
	if v := getStr(doc, "openapi"); v != "3.0.3" {
		t.Errorf("openapi version must be \"3.0.3\", got %q", v)
	}
}

// [P1] S21: the spec is a structurally valid OpenAPI doc (openapi/info/paths present).
func TestAC1_SpecStructurallyValid(t *testing.T) {
	root := repoRoot(t)
	doc := loadSpec(t, root)
	for _, k := range []string{"openapi", "info", "paths"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("spec missing required top-level key %q", k)
		}
	}
	paths := getMap(doc, "paths")
	if len(paths) == 0 {
		t.Errorf("paths is empty — still the `paths: {}` placeholder")
	}
}

// [P0] AC1: GET /health → 200 with a static HealthStatus { status } shape.
// DB-connectivity reporting is a later change, so `db` MUST NOT be present yet.
func TestAC1_HealthEndpointStaticShape(t *testing.T) {
	root := repoRoot(t)
	doc := loadSpec(t, root)
	get := getMap(getMap(getMap(doc, "paths"), "/health"), "get")
	if get == nil {
		t.Fatalf("GET /health is not defined in the spec")
	}
	schema := jsonResponseSchema(doc, get, "200")
	props := schemaProps(doc, schema)
	if props == nil {
		t.Fatalf("GET /health 200 response has no resolvable object schema")
	}
	if _, ok := props["status"]; !ok {
		t.Errorf("health 200 schema must have a `status` property")
	}
	if _, ok := props["db"]; ok {
		t.Errorf("health 200 schema must NOT expose `db` yet (DB connectivity is a later change)")
	}
}

// [P0] AC1: the Error envelope { code, message, details? } exists and `code` is a
// closed enum containing the SCREAMING_SNAKE error-code registry.
func TestAC1_ErrorEnvelopeAndCodeEnum(t *testing.T) {
	root := repoRoot(t)
	doc := loadSpec(t, root)
	errSchema := getMap(getMap(getMap(doc, "components"), "schemas"), "Error")
	if errSchema == nil {
		t.Fatalf("components.schemas.Error is not defined")
	}
	props := getMap(errSchema, "properties")
	for _, p := range []string{"code", "message", "details"} {
		if _, ok := props[p]; !ok {
			t.Errorf("Error schema must have property %q", p)
		}
	}
	codeEnum := enumStrings(getMap(props, "code"))
	if len(codeEnum) == 0 {
		t.Fatalf("Error.code must be a typed enum, not free text")
	}
	if missing := containsAll(codeEnum, errorCodeRegistry); len(missing) > 0 {
		t.Errorf("Error.code enum is missing registry codes: %v", missing)
	}
}

// [P1] AC1/S5: INVALID_SCAN_ID is present in the code enum (UUIDv7 rejection path).
func TestAC1_InvalidScanIdInCodeEnum(t *testing.T) {
	root := repoRoot(t)
	doc := loadSpec(t, root)
	props := getMap(getMap(getMap(getMap(doc, "components"), "schemas"), "Error"), "properties")
	if missing := containsAll(enumStrings(getMap(props, "code")), []string{"INVALID_SCAN_ID"}); len(missing) > 0 {
		t.Errorf("Error.code enum must include INVALID_SCAN_ID (UUIDv7 reject path)")
	}
}

// [P0] AC3: validation errors are a TYPED array `details: [{field, code, message}]`
// — a ValidationErrorDetail schema, not additionalProperties/freeform.
func TestAC3_TypedValidationErrorDetails(t *testing.T) {
	root := repoRoot(t)
	doc := loadSpec(t, root)

	detail := findSchemaWithProps(doc, "field", "code", "message")
	if detail == nil {
		t.Fatalf("no ValidationErrorDetail-shaped schema { field, code, message } found in components.schemas (AC3)")
	}

	// Some schema must carry `details` as an ARRAY of that detail schema.
	schemas := getMap(getMap(doc, "components"), "schemas")
	typedArrayFound := false
	for _, v := range schemas {
		details := getMap(asMap(v), "properties")["details"]
		dm := asMap(details)
		if dm == nil || getStr(dm, "type") != "array" {
			continue
		}
		items := getMap(dm, "items")
		itemProps := schemaProps(doc, items)
		if len(containsAll(mapKeys(itemProps), []string{"field", "code", "message"})) == 0 {
			typedArrayFound = true
			break
		}
	}
	if !typedArrayFound {
		t.Errorf("validation `details` must be a typed array of { field, code, message } (AC3), not freeform")
	}
}

// ===========================================================================
// AC1 — CONTRACT LEVEL: the ratified 202 batch-sync scan contract.
// ===========================================================================

// [P0] AC1: POST /api/v1/scans responds 202 with { results: [{ id, status, reason? }] }
// and status ∈ {applied, duplicate, rejected}.
func TestAC1_ScanBatch202ResponseContract(t *testing.T) {
	root := repoRoot(t)
	doc := loadSpec(t, root)
	post := getMap(getMap(getMap(doc, "paths"), "/api/v1/scans"), "post")
	if post == nil {
		t.Fatalf("POST /api/v1/scans is not defined in the spec")
	}
	respSchema := jsonResponseSchema(doc, post, "202")
	if respSchema == nil {
		t.Fatalf("POST /api/v1/scans must define a 202 application/json response (accept-and-enqueue)")
	}
	resultsProp := getMap(schemaProps(doc, respSchema), "results")
	if getStr(resultsProp, "type") != "array" {
		t.Fatalf("202 response must carry a `results` array")
	}
	itemProps := schemaProps(doc, getMap(resultsProp, "items"))
	for _, p := range []string{"id", "status"} {
		if _, ok := itemProps[p]; !ok {
			t.Errorf("scan result item must have property %q", p)
		}
	}
	statusEnum := enumStrings(getMap(itemProps, "status"))
	if missing := containsAll(statusEnum, []string{"applied", "duplicate", "rejected"}); len(missing) > 0 {
		t.Errorf("result `status` enum must include applied|duplicate|rejected; missing %v", missing)
	}
}

// [P0] AC1: the request is a BATCH of scan items using snake_case wire names,
// each with id, event_id, slot, method (barcode|qr|nfc), client_scanned_at.
func TestAC1_ScanBatchRequestContract(t *testing.T) {
	root := repoRoot(t)
	doc := loadSpec(t, root)
	post := getMap(getMap(getMap(doc, "paths"), "/api/v1/scans"), "post")
	if post == nil {
		t.Fatalf("POST /api/v1/scans is not defined in the spec")
	}

	// A scan-item schema with the locked snake_case fields must exist.
	item := findSchemaWithProps(doc, "id", "event_id", "slot", "method", "client_scanned_at")
	if item == nil {
		t.Fatalf("no scan-item schema with snake_case { id, event_id, slot, method, client_scanned_at } found (JSON casing is LOCKED)")
	}
	methodEnum := enumStrings(getMap(getMap(item, "properties"), "method"))
	if missing := containsAll(methodEnum, []string{"barcode", "qr", "nfc"}); len(missing) > 0 {
		t.Errorf("scan `method` enum must include barcode|qr|nfc; missing %v", missing)
	}

	// Option B (chosen): the request wraps the batch under a `scans` array property —
	// { "scans": [ <scan item>, ... ] } — leaving room for future top-level fields
	// (e.g. device_id). A bare top-level array is NOT accepted.
	req := jsonRequestSchema(post)
	reqSchema := resolveSchema(doc, req)
	if reqSchema == nil {
		t.Fatalf("POST /api/v1/scans must define an application/json requestBody schema")
	}
	scans := asMap(getMap(reqSchema, "properties")["scans"])
	if scans == nil || getStr(scans, "type") != "array" {
		t.Errorf("scan request must wrap the batch under a `scans` array property, e.g. { \"scans\": [ ... ] }")
	}
}

// [P1] AC1/S5: UUIDv7 idempotency semantics are documented on the scan-item `id`.
func TestAC1_ScanIdDocumentsUUIDv7(t *testing.T) {
	root := repoRoot(t)
	doc := loadSpec(t, root)
	item := findSchemaWithProps(doc, "id", "event_id", "slot", "method", "client_scanned_at")
	if item == nil {
		t.Fatalf("scan-item schema not found (see TestAC1_ScanBatchRequestContract)")
	}
	desc := strings.ToLower(getStr(getMap(getMap(item, "properties"), "id"), "description"))
	if !strings.Contains(desc, "uuidv7") && !strings.Contains(desc, "uuid v7") && !strings.Contains(desc, "uuid7") {
		t.Errorf("scan-item `id` description must document UUIDv7 idempotency semantics")
	}
}

// ===========================================================================
// STATIC / CONFIG LEVEL — codegen wiring must be real and pinned.
// ===========================================================================

// [P1] S8: contracts/oapi-codegen.yaml exists with package/output/generate config.
func TestStatic_OapiCodegenConfigExists(t *testing.T) {
	root := repoRoot(t)
	requireFile(t, root, "contracts/oapi-codegen.yaml")
	if exists(filepath.Join(root, "contracts", "oapi-codegen.yaml")) {
		requireContains(t, root, "contracts/oapi-codegen.yaml", "package", "output", "generate")
	}
}

// [P1] S9: the Makefile invokes REAL, PINNED generators — no `@echo` stub and no
// `@latest` (non-reproducible → drift-gate flakes).
func TestStatic_MakefileRealPinnedCodegen(t *testing.T) {
	root := repoRoot(t)
	mk := mustReadFile(t, filepath.Join(root, "contracts", "Makefile"))
	if !strings.Contains(mk, "oapi-codegen") {
		t.Errorf("contracts/Makefile must invoke oapi-codegen for Go generation")
	}
	if !strings.Contains(mk, "openapi-typescript") {
		t.Errorf("contracts/Makefile must invoke openapi-typescript for TS generation")
	}
	if strings.Contains(mk, "oapi-codegen@latest") || strings.Contains(mk, "openapi-typescript@latest") {
		t.Errorf("generators must be version-PINNED, never @latest (drift-gate reproducibility)")
	}
	if strings.Contains(mk, "(stub)") {
		t.Errorf("contracts/Makefile is still the @echo stub")
	}
}

// [P1] S10: openapi-typescript is pinned (^7) in the ROOT package.json devDependencies.
func TestStatic_RootPinsOpenapiTypescript(t *testing.T) {
	root := repoRoot(t)
	pkg := mustReadFile(t, filepath.Join(root, "package.json"))
	if !strings.Contains(pkg, "openapi-typescript") {
		t.Errorf("root package.json must pin openapi-typescript as a devDependency")
	}
}

// [P1] S15: contract-drift.yml sets up BOTH toolchains and runs the real gate.
func TestStatic_ContractDriftWorkflowRealGate(t *testing.T) {
	root := repoRoot(t)
	requireContains(t, root, ".github/workflows/contract-drift.yml",
		"actions/setup-go", "pnpm/action-setup", "actions/setup-node",
		"pnpm install", "make", "generate", "git diff --exit-code")
}

// [P1] S19: .gitattributes normalizes EOL so the drift-gate can't false-positive
// on CRLF/LF churn.
func TestStatic_GitattributesEOLNormalization(t *testing.T) {
	root := repoRoot(t)
	requireFile(t, root, ".gitattributes")
	if exists(filepath.Join(root, ".gitattributes")) {
		requireContains(t, root, ".gitattributes", "text=auto", "eol=lf", "contracts/generated")
	}
}

// [P0] S7: generated Go AND TS files carry the DO NOT EDIT marker.
func TestStatic_GeneratedFilesCarryDoNotEdit(t *testing.T) {
	root := repoRoot(t)
	assertRealCodegen(t, root)
	if exists(filepath.Join(root, "contracts", "generated", "ts", "schema.ts")) {
		requireContains(t, root, "contracts/generated/ts/schema.ts", "DO NOT EDIT")
	}
}

// [P2] S20: regression guards — module path + TS package name are preserved
// (renaming either breaks api/ and mobile/web import wiring).
func TestGuard_GeneratedIdentityPreserved(t *testing.T) {
	root := repoRoot(t)
	requireContains(t, root, "contracts/generated/go/go.mod",
		"module github.com/Shruhath/OD-Amrita/contracts/generated/go")
	requireContains(t, root, "contracts/generated/ts/package.json", "@od-amrita/contracts")
}

// ===========================================================================
// INTEGRATION LEVEL — real codegen, idempotency, and the drift-gate.
// ===========================================================================

// [P0] S6: `make -C contracts generate` runs real codegen (exit 0) and writes
// real Go + TS output.
func TestIntegration_MakeGenerateProducesRealOutput(t *testing.T) {
	root := repoRoot(t)
	requireTool(t, "make")
	if out, err := runCmd(t, root, "make", "-C", "contracts", "generate"); err != nil {
		t.Fatalf("`make -C contracts generate` must succeed: %v\n%s", err, out)
	}
	assertRealCodegen(t, root)
}

// [P0] S11 + S12: generation is idempotent — a clean spec generate leaves
// `git diff --exit-code` clean, and running generate twice is byte-stable.
func TestIntegration_GenerateIsIdempotentAndClean(t *testing.T) {
	root := repoRoot(t)
	requireTool(t, "make")
	requireTool(t, "git")
	guardCleanTree(t, root, "contracts/generated")
	assertRealCodegen(t, root) // ensures we are exercising real output, not a stub

	// Two consecutive generates must both leave the tree clean.
	for i := 0; i < 2; i++ {
		if out, err := runCmd(t, root, "make", "-C", "contracts", "generate"); err != nil {
			t.Fatalf("generate #%d failed: %v\n%s", i+1, err, out)
		}
	}
	if out, err := runCmd(t, root, "git", "diff", "--exit-code", "--", "contracts/generated"); err != nil {
		t.Errorf("generated output is not idempotent — `git diff --exit-code` is dirty after regenerate:\n%s", out)
	}
}

// [P0] S13 (negative, self-restoring): hand-editing a generated file makes the
// drift-gate FAIL. This also proves generated files are git-TRACKED (not ignored).
func TestIntegration_DriftGateFailsOnHandEditedGeneratedFile(t *testing.T) {
	root := repoRoot(t)
	requireTool(t, "git")
	assertRealCodegen(t, root)

	target := firstGeneratedGoFile(t, root)
	if target == "" {
		t.Fatalf("no generated Go file to mutate")
	}
	rel, _ := filepath.Rel(root, target)
	guardCleanTree(t, root, rel)

	original := mustReadFile(t, target)
	t.Cleanup(func() { _ = os.WriteFile(target, []byte(original), 0o644) })

	if err := os.WriteFile(target, []byte(original+"\n// drift probe\n"), 0o644); err != nil {
		t.Fatalf("cannot mutate %s: %v", rel, err)
	}
	if _, err := runCmd(t, root, "git", "diff", "--exit-code", "--", rel); err == nil {
		t.Errorf("drift-gate must FAIL (non-zero) on a hand-edited generated file, but `git diff --exit-code` was clean")
	}
}

// [P0] S14 (negative, self-restoring): changing the spec without regenerating is
// caught by the gate — the working tree is dirty for contracts/openapi.yaml.
func TestIntegration_DriftGateFailsOnStaleSpec(t *testing.T) {
	root := repoRoot(t)
	requireTool(t, "git")
	assertRealCodegen(t, root)

	spec := filepath.Join(root, "contracts", "openapi.yaml")
	guardCleanTree(t, root, "contracts/openapi.yaml")

	original := mustReadFile(t, spec)
	t.Cleanup(func() { _ = os.WriteFile(spec, []byte(original), 0o644) })

	if err := os.WriteFile(spec, []byte(original+"\n# drift probe (edit without regenerating)\n"), 0o644); err != nil {
		t.Fatalf("cannot mutate spec: %v", err)
	}
	if _, err := runCmd(t, root, "git", "diff", "--exit-code", "--", "contracts/openapi.yaml"); err == nil {
		t.Errorf("drift-gate must FAIL when the spec changes without a regenerate, but `git diff --exit-code` was clean")
	}
}

// [P0] S17: with populated generated Go, the api module + workspace still build.
func TestIntegration_ApiBuildsWithGeneratedGo(t *testing.T) {
	root := repoRoot(t)
	requireTool(t, "go")
	assertRealCodegen(t, root)
	if out, err := runCmd(t, filepath.Join(root, "api"), "go", "build", "./..."); err != nil {
		t.Errorf("`go build ./...` in api/ must pass with the populated generated Go module: %v\n%s", err, out)
	}
}

// [P0] S18: with populated generated TS, a consumer (web) type-checks clean and
// the generated schema is re-exported from the @od-amrita/contracts entrypoint.
func TestIntegration_WebTypechecksWithGeneratedTs(t *testing.T) {
	root := repoRoot(t)
	assertRealCodegen(t, root)
	// index.ts must re-export the generated schema so consumers import via workspace:*.
	requireContains(t, root, "contracts/generated/ts/index.ts", "schema")
	if strings.TrimSpace(mustReadFile(t, filepath.Join(root, "contracts", "generated", "ts", "index.ts"))) == "export {}" {
		t.Errorf("contracts/generated/ts/index.ts is still the `export {}` placeholder")
	}

	requireTool(t, "pnpm")
	if out, err := runCmd(t, root, "pnpm", "-C", "web", "run", "typecheck"); err != nil {
		t.Errorf("`pnpm -C web run typecheck` (tsc --noEmit) must pass with generated TS types: %v\n%s", err, out)
	}
}

// ---------------------------------------------------------------------------
// Integration test helpers
// ---------------------------------------------------------------------------

// guardCleanTree skips (does not fail) a mutation-based test if the given path is
// already dirty, so the harness never compounds pre-existing local changes.
func guardCleanTree(t *testing.T, root, rel string) {
	t.Helper()
	out, err := runCmd(t, root, "git", "status", "--porcelain", "--", rel)
	if err != nil {
		t.Skipf("cannot query git status for %s: %v", rel, err)
	}
	if strings.TrimSpace(out) != "" {
		t.Skipf("%s already has uncommitted changes; skipping mutation test to avoid clobbering local work", rel)
	}
}

// firstGeneratedGoFile returns an absolute path to a generated Go file (excluding
// the placeholder doc.go if real files exist), or "".
func firstGeneratedGoFile(t *testing.T, root string) string {
	t.Helper()
	goDir := filepath.Join(root, "contracts", "generated", "go")
	entries, _ := os.ReadDir(goDir)
	var fallback string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		p := filepath.Join(goDir, e.Name())
		if e.Name() == "doc.go" {
			fallback = p
			continue
		}
		return p
	}
	return fallback
}
