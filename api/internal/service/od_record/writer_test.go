package odrecord

import (
	"errors"
	"testing"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapPgErrorMapsEventForeignKeyRaceToEventNotFound(t *testing.T) {
	err := mapPgError(&pgconn.PgError{
		Code:           sqlstateForeignKeyViolation,
		ConstraintName: "od_records_event_id_fkey",
	})
	if !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("error = %v, want ErrEventNotFound", err)
	}
}

// The other half of AC 7: the database refusing an out-of-range od_date must
// surface as VALIDATION_FAILED on the field, NOT as a 500.
//
// The trigger itself is proven against a real database in
// api/test/db/multi_day_schema_test.go — but that test writes raw SQL as the
// runtime role and never reaches this function, so until now nothing anywhere
// asserted the resulting error CODE. That is structurally the same trap as the
// renamed unique index one branch above: a constraint name matched as a string
// literal, with the mapping unproven. Story 0.12b reuses this path verbatim the
// moment a teacher is allowed to pick the day, which is when an unmapped 500
// would start reaching real users.
func TestMapPgErrorMapsTheEventRangeGuardToAFieldValidation(t *testing.T) {
	err := mapPgError(&pgconn.PgError{
		Code:           sqlstateCheckViolation,
		ConstraintName: "od_records_od_date_event_range_ck",
	})

	var de domain.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("error = %v (%T), want a domain.DomainError", err, err)
	}
	if de.Code != contracts.ErrorCodeVALIDATIONFAILED {
		t.Fatalf("code = %s, want %s", de.Code, contracts.ErrorCodeVALIDATIONFAILED)
	}
	if len(de.Details) != 1 {
		t.Fatalf("details = %#v, want exactly one field detail", de.Details)
	}
	if de.Details[0].Field != "od_date" || de.Details[0].Code != "outside_event_range" {
		t.Fatalf("detail = %+v, want field od_date / code outside_event_range", de.Details[0])
	}
}

// A check violation this service does NOT own must stay a 500 rather than being
// dressed up as a field error on od_date. Without this, the branch above could
// be widened to "any 23514" and nothing would notice.
func TestMapPgErrorLeavesAnUnknownCheckViolationUnmapped(t *testing.T) {
	cause := &pgconn.PgError{
		Code:           sqlstateCheckViolation,
		ConstraintName: "od_records_some_other_ck",
	}
	err := mapPgError(cause)

	var de domain.DomainError
	if errors.As(err, &de) {
		t.Fatalf("error = %v, want the raw pg error rather than a domain error", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want the original cause", err)
	}
}

// The FR11 duplicate path, asserted on the CONSTRAINT NAME rather than only on
// "an error happened". This is the T8 trap: renaming the index in the migration
// without changing the literal in mapPgError turns a 409 into an unmapped 500
// while every test that merely asserts the second insert failed stays green.
func TestMapPgErrorMapsTheDayKeyedDuplicateIndexToScanDuplicate(t *testing.T) {
	err := mapPgError(&pgconn.PgError{
		Code:           sqlstateUniqueViolation,
		ConstraintName: "uq_od_records_present_student_event_date_slot",
	})
	if !errors.Is(err, ErrDuplicatePresentRecord) {
		t.Fatalf("error = %v, want ErrDuplicatePresentRecord", err)
	}

	// The pre-0.12a name must NOT still map — it no longer exists in the schema,
	// and leaving it matched would hide exactly the rename this story performed.
	stale := &pgconn.PgError{
		Code:           sqlstateUniqueViolation,
		ConstraintName: "uq_od_records_present_student_event_slot",
	}
	if errors.Is(mapPgError(stale), ErrDuplicatePresentRecord) {
		t.Fatal("the pre-0.12a index name still maps to ErrDuplicatePresentRecord; 0016 dropped that index")
	}
}

// The manual-entry reason guard, mapped after it produced a real outage.
//
// od_records_manual_reason_note_ck read `actor_role = 'coordinator' OR
// reason_note IS NOT NULL` from the day it was written, which was an exact
// statement of the rule while the only non-coordinator mark was a manual search
// entry. Giving admins the live marking screen broke that equivalence: their nfc
// and camera marks are note-free by design, so every admin card tap violated the
// constraint, and because the name was unmapped here the operator saw a bare
// 500 with nothing logged. Production had never accepted one.
//
// 0026 corrects the predicate, so this branch should now be unreachable. It is
// mapped and pinned anyway, because "unreachable" is exactly what was believed
// about it before, and the next drift between the service's idea of a manual
// mark and the schema's should cost a 400 naming the field rather than a 500
// naming nothing.
func TestMapPgErrorMapsTheManualReasonGuardToAFieldValidation(t *testing.T) {
	err := mapPgError(&pgconn.PgError{
		Code:           sqlstateCheckViolation,
		ConstraintName: "od_records_manual_reason_note_ck",
	})

	var de domain.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("error = %v (%T), want a domain.DomainError", err, err)
	}
	if de.Code != contracts.ErrorCodeVALIDATIONFAILED {
		t.Fatalf("code = %s, want %s", de.Code, contracts.ErrorCodeVALIDATIONFAILED)
	}
	if len(de.Details) != 1 {
		t.Fatalf("details = %#v, want exactly one field detail", de.Details)
	}
	if de.Details[0].Field != "reason_note" || de.Details[0].Code != "required" {
		t.Fatalf("detail = %+v, want field reason_note / code required", de.Details[0])
	}
}
