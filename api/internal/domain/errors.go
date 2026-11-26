// Package domain holds core domain types shared across services: the audit-event
// registry (events.go) and the canonical domain-error registry (this file).
package domain

import (
	"fmt"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
)

// errors.go ★ — the canonical domain-error registry.
//
// DomainError is a sentinel domain error carrying a canonical, machine-branchable
// error code plus a human message. The Code is the GENERATED contract enum
// (contracts.ErrorCode) — the wire SSOT declared in openapi.yaml — so the codes
// are referenced, never re-declared in Go: one definition, zero drift. Services
// return a DomainError; the gin error-middleware (middleware/error, deliberately
// NOT wired here) maps Code to an HTTP status via HTTPStatus and renders the {code, message, details} envelope.
//
// Error() has a VALUE receiver so a DomainError value satisfies error — callers
// can `return DomainError{Code: …, Message: …}` directly.
type DomainError struct {
	Code    contracts.ErrorCode
	Message string
	Details []contracts.ValidationErrorDetail
}

func (e DomainError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// codeToStatus is the SINGLE centralized code -> HTTP-status map. Its keys are
// the generated contract codes (the wire SSOT), so a code renamed/removed in
// openapi.yaml (then regenerated) makes this file fail to COMPILE, forcing the
// map to be updated in the same change. The parity test (errors_test.go) is the
// CI backstop: it fails if any contract code lacks a mapping, if a mapped key is
// no longer .Valid() per the contract, or if the counts diverge.
//
// The status choices that were open judgement calls are settled here:
//   - AUTH_DOMAIN_REJECTED -> 403: the identity is known (Microsoft login
//     succeeded) but the email domain is not permitted; re-authenticating cannot
//     help, so Forbidden fits better than 401 Unauthorized.
//   - SLOT_CLOSED          -> 409: the write conflicts with the slot's closed
//     state (same family as SCAN_DUPLICATE's 409).
//   - CARD_NOT_RECOGNIZED  -> 422: the token is well-formed but maps to no known
//     card — semantically unprocessable rather than syntactically malformed (400).
//   - EVENT_ARCHIVED       -> 409: the request is well-formed and authorized, but
//     the event aggregate is closed and accepts no further child writes. Same
//     state-conflict family as SCAN_DUPLICATE/SLOT_CLOSED, and deliberately
//     distinct from EVENT_NOT_FOUND's 404 — "the id is wrong" and "the aggregate
//     is closed" are different answers the portal branches on differently.
//   - USER_NOT_FOUND        -> 404: no `users` row matches the supplied email.
//     Teacher-profile creation elevates an existing identity; there is
//     genuinely no prior code for "no such user".
//   - ROLE_ALREADY_ASSIGNED -> 409: the user already holds a non-student role.
//     Same state-conflict family as EVENT_ARCHIVED/SCAN_DUPLICATE, not reused
//     from EVENT_ARCHIVED because it is a distinct aggregate/state.
//   - COORDINATOR_ASSIGNMENT_EXISTS -> 409: the assignment already exists for
//     that user/event pair, including a uniqueness race.
//   - SELF_DEMOTION_REFUSED / LAST_ADMIN_DEMOTION_REFUSED -> 409: the request is
//     authorized and well-formed, but the user aggregate's state forbids it — an
//     admin may not strip their own access, and the system may not be left with
//     no admin at all. Same state-conflict family as ROLE_ALREADY_ASSIGNED.
var codeToStatus = map[contracts.ErrorCode]int{
	contracts.ErrorCodeAUTHDOMAINREJECTED:        403,
	contracts.ErrorCodeAUTHTOKENINVALID:          401,
	contracts.ErrorCodeRBACFORBIDDEN:             403,
	contracts.ErrorCodeSCANDUPLICATE:             409,
	contracts.ErrorCodeSLOTCLOSED:                409,
	contracts.ErrorCodeCARDNOTRECOGNIZED:         422,
	contracts.ErrorCodeCAPTURECREDITLIMITREACHED: 429,
	contracts.ErrorCodeQRLIMITREACHED:            429,
	contracts.ErrorCodeRATELIMITED:               429,
	contracts.ErrorCodeINVALIDSCANID:             400,
	contracts.ErrorCodeVALIDATIONFAILED:          400,
	contracts.ErrorCodeEVENTNOTFOUND:             404,
	contracts.ErrorCodeEVENTARCHIVED:             409,
	contracts.ErrorCodeCATEGORYNOTFOUND:          404,
	// A record id is absent versus a known record being terminal; clients need
	// this ordinary missing-resource/state-conflict distinction to recover safely.
	contracts.ErrorCodeODRECORDNOTFOUND:            404,
	contracts.ErrorCodeODRECORDALREADYREMOVED:      409,
	contracts.ErrorCodeUSERNOTFOUND:                404,
	contracts.ErrorCodeSTUDENTNOTFOUND:             404,
	contracts.ErrorCodeROLEALREADYASSIGNED:         409,
	contracts.ErrorCodeCOORDINATORASSIGNMENTEXISTS: 409,
	contracts.ErrorCodeSELFDEMOTIONREFUSED:         409,
	contracts.ErrorCodeLASTADMINDEMOTIONREFUSED:    409,
	contracts.ErrorCodeDISPUTEIDENTITYUNVERIFIED:   400,
	contracts.ErrorCodeDISPUTEALREADYOPEN:          409,
	contracts.ErrorCodeDISPUTENOTFOUND:             404,
	contracts.ErrorCodeDISPUTEALREADYRESOLVED:      409,
	contracts.ErrorCodeINTERNAL:                    500,
}

// HTTPStatus returns the HTTP status for a domain error code. An unknown or
// zero-value code maps to 500 — never a panic, never a misleading 4xx — so a
// code that somehow reaches the mapper without an entry fails safe.
func HTTPStatus(code contracts.ErrorCode) int {
	if status, ok := codeToStatus[code]; ok {
		return status
	}
	return 500
}
