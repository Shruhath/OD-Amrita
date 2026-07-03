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
// KEY DESIGN NOTE — allowed-status sets, not single pins:
// The architecture's HTTP-status vocabulary (architecture#Format lines 483-484)
// is 200/201/202/400/401/403/404/409/429/5xx. Three codes are left as a genuine
// design choice in the story (AUTH_DOMAIN_REJECTED 401|403, SLOT_CLOSED 409|422,
// CARD_NOT_RECOGNIZED 400|422). For those this test accepts EITHER defensible
// value — it must not pin a number the story hasn't settled. The other seven are
// unambiguous and pinned exactly.

import (
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
	{contracts.ErrorCodeRBACFORBIDDEN, []int{403}},           // arch: 403 RBAC
	{contracts.ErrorCodeSCANDUPLICATE, []int{409}},           // arch: 409 duplicate
	{contracts.ErrorCodeSLOTCLOSED, []int{409, 422}},         // conflict vs unprocessable — dev's call
	{contracts.ErrorCodeCARDNOTRECOGNIZED, []int{400, 422}},  // validation vs unprocessable — dev's call
	{contracts.ErrorCodeQRLIMITREACHED, []int{429}},          // arch: 429 rate-limited
	{contracts.ErrorCodeRATELIMITED, []int{429}},             // arch: 429 rate-limited
	{contracts.ErrorCodeINVALIDSCANID, []int{400}},           // arch: 400 validation
	{contracts.ErrorCodeVALIDATIONFAILED, []int{400}},        // arch: 400 validation
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

// TestCodeToStatus_ParityWithContractEnum is the drift machine-teeth. It proves
// the hand-written status map and the GENERATED contract enum stay in lockstep:
//
//   - every generated code has a mapping (a NEW spec code without a mapping fails);
//   - every mapped key is .Valid() per the contract (a removed/renamed code, once
//     regenerated, leaves a stale key that stops being .Valid() and fails);
//   - the counts match (an extra/duplicate key fails).
//
// Referencing the generated constants by identifier also means a code deleted or
// renamed in openapi.yaml (then regenerated) makes THIS FILE fail to compile —
// forcing the mapping to be updated in the same change. That is the point.
func TestCodeToStatus_ParityWithContractEnum(t *testing.T) {
	// The generated contract codes, referenced by their generated identifiers.
	contractCodes := make([]contracts.ErrorCode, 0, len(codeStatusCases))
	for _, tc := range codeStatusCases {
		contractCodes = append(contractCodes, tc.code)
	}

	// Every contract code must have a status mapping.
	for _, code := range contractCodes {
		if !code.Valid() {
			t.Errorf("[P0] %q is not .Valid() per the generated contract — the test's contractCodes list is stale", string(code))
		}
		if _, ok := codeToStatus[code]; !ok {
			t.Errorf("[P0] codeToStatus is missing a mapping for contract code %q", string(code))
		}
	}

	// Every mapped key must be a real contract code (catches a stale key left
	// behind after a code is renamed/removed in the spec + regenerated).
	for code := range codeToStatus {
		if !code.Valid() {
			t.Errorf("[P0] codeToStatus contains %q which is NOT a valid contract code — the enum drifted from the map", string(code))
		}
	}

	// Counts must match: no extras, no gaps.
	if len(codeToStatus) != len(contractCodes) {
		t.Errorf("[P0] codeToStatus has %d entries but the contract defines %d codes — a code was added/removed without updating the map (or this test)", len(codeToStatus), len(contractCodes))
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
