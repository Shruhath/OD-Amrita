package odrecord

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
)

func TestValidateManagementReasonRejectsBlankAndOverlongNotes(t *testing.T) {
	for _, note := range []string{"", " \t\n ", strings.Repeat("x", 2001)} {
		err := validateManagementReason(note)
		var domainErr domain.DomainError
		if !errors.As(err, &domainErr) || domainErr.Code != contracts.ErrorCodeVALIDATIONFAILED {
			t.Fatalf("validateManagementReason(%d chars) = %v, want VALIDATION_FAILED", len(note), err)
		}
	}
	if err := validateManagementReason("  student verified at venue  "); err != nil {
		t.Fatalf("validateManagementReason(valid) = %v", err)
	}
}

func TestManagementActorAndIDRequiresTeacherOrAdministratorAndAUUID(t *testing.T) {
	validID := uuid.NewString()
	if _, err := managementActorAndID(Actor{Role: "coordinator"}, validID); err == nil {
		t.Fatal("coordinator was admitted to record management")
	}
	if _, err := managementActorAndID(Actor{Role: "teacher"}, "not-a-uuid"); err == nil {
		t.Fatal("invalid record id was admitted")
	}
	if got, err := managementActorAndID(Actor{Role: "admin"}, validID); err != nil || got.String() != validID {
		t.Fatalf("admin management identity = %s, %v; want %s, nil", got, err, validID)
	}
}
