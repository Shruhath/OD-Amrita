package domain

// category_registry_test.go — Story 2.2 coverage for AC 6 and 8.
// UNIT test: no build tag, no database. It runs in the default `go test ./...`.
//
// Invoked by: api-ci -> build job.
//
//
// Both facts asserted here are the halves that CONVENTIONS requires to move
// together, and that fail loudly at runtime if only one lands:
//   - store.WithAudit rejects an unregistered action, so a missing AllActions
//     entry turns every category create into a 500 at the write boundary.
//   - HTTPStatus falls back to 500 for an unmapped code, so a missing
//     codeToStatus entry silently downgrades AC 4's 409 into a server error.

import (
	"slices"
	"testing"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
)

const (
	activateCategoryAction = "2.2 Task 4 — activate when ActionCategoryCreated is added to events.go and AllActions"
	activateEventArchived  = "2.2 Task 2 — activate when EVENT_ARCHIVED is mapped in codeToStatus"
)

func TestCategoryCreatedIsARegisteredAuditAction(t *testing.T) {
	const want Action = "category.created"

	// Registered() is derived from AllActions, so this single assertion proves
	// both halves: the constant exists AND it was appended to the registry.
	if !want.Registered() {
		t.Errorf("action %q is not registered; store.WithAudit will reject every category create", want)
	}
	if !slices.Contains(AllActions, want) {
		t.Errorf("AllActions must contain %q", want)
	}
}

func TestCategoryCreatedUsesThePastTenseDottedWireName(t *testing.T) {
	// The string value is written verbatim into the immutable ledger, so it is a
	// permanent wire commitment: past-tense, dotted, <entity>.<verb>. Renaming it
	// later would orphan every row already written under the old name.
	var found Action
	for _, action := range AllActions {
		if action == "category.created" {
			found = action
		}
	}
	if found == "" {
		t.Fatal("no category action registered")
	}
	if string(found) != "category.created" {
		t.Errorf("category action wire name = %q, want category.created", found)
	}
	// Guard against a plural or a namespaced variant sneaking in beside it.
	for _, wrong := range []Action{"categories.created", "event.category_created", "category.create"} {
		if slices.Contains(AllActions, wrong) {
			t.Errorf("AllActions contains the wrong category action %q", wrong)
		}
	}
}

func TestEventArchivedMapsToStateConflict(t *testing.T) {
	const code = contracts.ErrorCode("EVENT_ARCHIVED")

	// 409 is the state-conflict family already used by SCAN_DUPLICATE and
	// SLOT_CLOSED: the request is well-formed and authorized, but the aggregate
	// is closed. An unmapped code returns 500 here, which would present AC 4's
	// deliberate rejection as a server fault.
	if got := HTTPStatus(code); got != 409 {
		t.Errorf("HTTPStatus(%s) = %d, want 409", code, got)
	}
	if _, ok := codeToStatus[code]; !ok {
		t.Errorf("codeToStatus must map %s explicitly rather than relying on the 500 fallback", code)
	}
}

func TestEventArchivedIsDistinctFromEventNotFound(t *testing.T) {
	// An unknown event and an archived event are different answers to different
	// questions, and the portal branches on them differently: 404 means the id is
	// wrong, 409 means the aggregate is closed. Collapsing them would make the
	// archived-event error indistinguishable from a bad link.
	archived := HTTPStatus(contracts.ErrorCode("EVENT_ARCHIVED"))
	notFound := HTTPStatus(contracts.ErrorCodeEVENTNOTFOUND)
	if archived == notFound {
		t.Errorf("EVENT_ARCHIVED and EVENT_NOT_FOUND must not share status %d", archived)
	}
	if notFound != 404 {
		t.Errorf("HTTPStatus(EVENT_NOT_FOUND) = %d, want 404 (Story 2.1 regression)", notFound)
	}
}
