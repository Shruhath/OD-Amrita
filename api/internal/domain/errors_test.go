package domain

// errors_test.go — RED-PHASE ATDD scaffold for Story 1.4, AC 5 (error-code
// registry + centralized code->HTTP-status map). UNIT test: no build tag, no DB.
//
// RED PHASE: `errors.go` is still a reserved stub, so DomainError, HTTPStatus and
// the unexported codeToStatus map do not exist yet. Until Task 4 lands them THIS
// PACKAGE FAILS TO COMPILE — the intended red signal.
//
// White-box (`package domain`) deliberately: the parity test reaches the
// UNEXPORTED codeToStatus map, which is the whole point — it proves the map is
// kept in lockstep with the GENERATED contract enum (the wire SSOT), so a code
// renamed/removed/added in openapi.yaml (then regenerated) breaks CI here.
//
// REAL TEETH FOR *ADDED* CODES: the parity test enumerates the ErrorCode enum by
// reading the generated contract SOURCE (types.gen.go), not a hand-maintained
// in-file copy — because oapi-codegen emits no enumerable set (only per-constant +
// a .Valid() switch). Renames/removals already break compilation here (the
// identifiers vanish); reading the generated set is what also catches a code
// ADDED to the contract but forgotten in codeToStatus. Stdlib-only on purpose (no
// YAML dependency → no go.mod tidy/no-network trap); the contract-drift gate
// already binds types.gen.go byte-for-byte to a regen from openapi.yaml.
//
// KEY DESIGN NOTE — allowed-status sets, not single pins:
// The architecture's HTTP-status vocabulary (architecture#Format lines 483-484)
// is 200/201/202/400/401/403/404/409/429/5xx. Three codes are left as a genuine
// design choice in the story (AUTH_DOMAIN_REJECTED 401|403, SLOT_CLOSED 409|422,
// CARD_NOT_RECOGNIZED 400|422). For those this test accepts EITHER defensible
// value — it must not pin a number the story hasn't settled. The other seven are
// unambiguous and pinned exactly.

import (
	"os"
	"regexp"
	"strings"
	"testing"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
)

// codeStatusCases is the table of every generated contract code and the HTTP
// status(es) that are acceptable for it. Single-element `allowed` == pinned;
// multi-element == an open design choice the dev finalizes in errors.go.
var codeStatusCases = []struct {
	code    contracts.ErrorCode
	allowed []int
}{
	{contracts.ErrorCodeAUTHDOMAINREJECTED, []int{401, 403}}, // auth-domain rejection: unauth vs forbidden — dev's call
	{contracts.ErrorCodeAUTHTOKENINVALID, []int{401}},        // 1.5a: token not acceptable (sig/exp/aud/iss) — pinned 401
	{contracts.ErrorCodeRBACFORBIDDEN, []int{403}},           // arch: 403 RBAC
	{contracts.ErrorCodeSCANDUPLICATE, []int{409}},           // arch: 409 duplicate
	{contracts.ErrorCodeSLOTCLOSED, []int{409, 422}},         // conflict vs unprocessable — dev's call
	{contracts.ErrorCodeCARDNOTRECOGNIZED, []int{400, 422}},  // validation vs unprocessable — dev's call
	{contracts.ErrorCodeQRLIMITREACHED, []int{429}},          // arch: 429 rate-limited
	{contracts.ErrorCodeRATELIMITED, []int{429}},             // arch: 429 rate-limited
	{contracts.ErrorCodeINVALIDSCANID, []int{400}},           // arch: 400 validation
	{contracts.ErrorCodeVALIDATIONFAILED, []int{400}},        // arch: 400 validation
	{contracts.ErrorCodeEVENTNOTFOUND, []int{404}},           // event archive target does not exist
	{contracts.ErrorCodeEVENTARCHIVED, []int{409}},           // 2.2: closed aggregate refuses child writes — pinned 409
	{contracts.ErrorCodeUSERNOTFOUND, []int{404}},            // 2.3: no users row matches the supplied email — pinned 404
	{contracts.ErrorCodeSTUDENTNOTFOUND, []int{404}},         // 0.3: no students row matches the supplied id — pinned 404
	{contracts.ErrorCodeROLEALREADYASSIGNED, []int{409}},     // 2.3: non-student role is a state conflict — pinned 409
	{contracts.ErrorCodeINTERNAL, []int{500}},                // arch: 5xx internal
}

// TestHTTPStatus_MapsEveryCode asserts HTTPStatus returns an acceptable status
// for every code the contract defines. A code with no mapping would fall through
// to the 500 default and (for codes whose allowed set excludes 500) fail here.
func TestHTTPStatus_MapsEveryCode(t *testing.T) {
	for _, tc := range codeStatusCases {
		got := HTTPStatus(tc.code)
		ok := false
		for _, want := range tc.allowed {
			if got == want {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("[P1] HTTPStatus(%q) = %d, want one of %v", string(tc.code), got, tc.allowed)
		}
	}
}

// TestHTTPStatus_UnknownDefaults500 asserts an unknown or zero-value code maps to
// 500 (never a panic, never a misleading 4xx). This is the safety default for a
// code that somehow reaches the mapper without an entry.
func TestHTTPStatus_UnknownDefaults500(t *testing.T) {
	if got := HTTPStatus(contracts.ErrorCode("NOT_A_REAL_CODE")); got != 500 {
		t.Errorf("[P1] HTTPStatus(unknown) = %d, want 500", got)
	}
	if got := HTTPStatus(contracts.ErrorCode("")); got != 500 {
		t.Errorf("[P1] HTTPStatus(zero-value) = %d, want 500", got)
	}
}

// contractErrorCodeConst matches each generated ErrorCode enum constant in the
// contract source, e.g. `ErrorCodeAUTHDOMAINREJECTED ErrorCode = "AUTH_DOMAIN_REJECTED"`.
// Anchoring the const NAME to the `ErrorCode` prefix keeps the sibling
// `ValidationErrorCode…` enum (which also ends in "ErrorCode") out of the match.
var contractErrorCodeConst = regexp.MustCompile(`(?m)^\s*ErrorCode[A-Z][A-Za-z0-9]*\s+ErrorCode\s*=\s*"([A-Z_]+)"`)

// parseContractErrorCodes enumerates the ErrorCode enum by reading the GENERATED
// contract source (types.gen.go) — the true generated set, not a hand-maintained
// copy. A code ADDED to openapi.yaml (then regenerated) appears here automatically,
// so the parity checks below fail if it lacks a codeToStatus mapping — closing the
// gap a hand-written table would silently miss.
func parseContractErrorCodes(t *testing.T) []contracts.ErrorCode {
	t.Helper()
	// api/internal/domain -> repo root -> contracts/generated/go/types.gen.go
	const genPath = "../../../contracts/generated/go/types.gen.go"
	src, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("[P0] read generated contract source %s: %v", genPath, err)
	}
	matches := contractErrorCodeConst.FindAllStringSubmatch(string(src), -1)
	codes := make([]contracts.ErrorCode, 0, len(matches))
	for _, m := range matches {
		codes = append(codes, contracts.ErrorCode(m[1]))
	}
	return codes
}

// TestCodeToStatus_ParityWithContractEnum is the drift machine-teeth. It enumerates
// the GENERATED contract enum (parseContractErrorCodes) and proves the status map
// stays in lockstep with it:
//
//   - every generated code has a mapping — a code ADDED to the contract without a
//     mapping fails here (the gap a hand-maintained list could not catch);
//   - every mapped key is .Valid() per the contract (a removed/renamed code, once
//     regenerated, leaves a stale key that stops being .Valid() and fails);
//   - the counts match the generated set (an extra/duplicate key fails);
//   - the in-file codeStatusCases table also matches the generated set, so
//     TestHTTPStatus_MapsEveryCode stays exhaustive.
//
// Renames/removals additionally make errors.go / this file fail to COMPILE (the
// generated identifiers vanish), forcing the fix in the same change.
func TestCodeToStatus_ParityWithContractEnum(t *testing.T) {
	contractCodes := parseContractErrorCodes(t)
	if len(contractCodes) == 0 {
		t.Fatal("[P0] parsed zero ErrorCode constants from the generated contract — the path or generated format changed; the parity gate is blind")
	}

	// Every generated contract code must be .Valid() and have a status mapping.
	// This is what catches a code ADDED to the contract without a mapping.
	for _, code := range contractCodes {
		if !code.Valid() {
			t.Errorf("[P0] %q parsed from generated source is not .Valid() — generator/regex drift", string(code))
		}
		if _, ok := codeToStatus[code]; !ok {
			t.Errorf("[P0] codeToStatus is missing a mapping for contract code %q — a code was added to openapi.yaml without a status mapping", string(code))
		}
	}

	// Every mapped key must be a real contract code (catches a stale key left
	// behind after a code is renamed/removed in the spec + regenerated).
	for code := range codeToStatus {
		if !code.Valid() {
			t.Errorf("[P0] codeToStatus contains %q which is NOT a valid contract code — the enum drifted from the map", string(code))
		}
	}

	// Counts must match the GENERATED set: no extras, no gaps.
	if len(codeToStatus) != len(contractCodes) {
		t.Errorf("[P0] codeToStatus has %d entries but the generated contract defines %d codes — a code was added/removed without updating the map", len(codeToStatus), len(contractCodes))
	}

	// Keep the in-file status table (codeStatusCases, which drives
	// TestHTTPStatus_MapsEveryCode) exhaustive too, so that test can't silently
	// fall behind a newly added contract code.
	if len(codeStatusCases) != len(contractCodes) {
		t.Errorf("[P0] codeStatusCases covers %d codes but the generated contract defines %d — add the new/removed code to the status table", len(codeStatusCases), len(contractCodes))
	}
}

// TestDomainError_ImplementsError asserts DomainError satisfies the error
// interface and that Error() surfaces the code (so logs/tests can identify it).
func TestDomainError_ImplementsError(t *testing.T) {
	// Compile-time proof that a DomainError VALUE implements error (drives a
	// value receiver on Error(), so services can return DomainError{...} directly).
	var _ error = DomainError{}

	de := DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "not allowed"}
	if de.Error() == "" {
		t.Error("[P2] DomainError.Error() returned an empty string")
	}
	if !strings.Contains(de.Error(), string(contracts.ErrorCodeRBACFORBIDDEN)) {
		t.Errorf("[P2] DomainError.Error() should reference the code %q, got %q", string(contracts.ErrorCodeRBACFORBIDDEN), de.Error())
	}
}
