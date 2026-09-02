package domain

// events_test.go — RED-PHASE ATDD scaffold for Story 1.4, AC 5 (audit-event
// registry). This is a UNIT test: no build tag, no database. It runs in the
// default `go test ./...`.
//
// RED PHASE: `events.go` is still a reserved stub, so the symbols referenced
// here (Action, AllActions, and the eight action constants) do not exist yet.
// Until Task 3 lands them, THIS PACKAGE FAILS TO COMPILE — that compile error
// IS the red signal for the registry layer (the pure-Go equivalent of a failing
// assertion). It turns green the moment the tiny `events.go` registry is added.
//
// Priority tags in test names/messages: [P0] critical · [P1] important · [P2] guard.

import (
	"os"
	"regexp"
	"testing"
)

// TestAllActions_MatchesDeclaredConstants pins TWO things that must never drift:
//
//  1. Each action CONSTANT has the exact wire string the architecture mandates
//     (architecture#Communication-Patterns lines 472-475). The string is what
//     gets written into the immutable audit ledger, so a typo here is a
//     permanent, tamper-evident record of the wrong thing.
//  2. AllActions (the canonical SSOT slice the audit helper validates against)
//     contains EXACTLY these constants — no missing entry (a story that adds a
//     constant but forgets AllActions), no unexpected extra, no duplicate, and
//     no empty Action.
func TestAllActions_MatchesDeclaredConstants(t *testing.T) {
	// The canonical registry-of-record. When a future story introduces a new
	// auditable action it MUST be added both to events.go AND to this map
	// (that per-story discipline is the CONVENTIONS rule this story establishes).
	want := map[Action]string{
		ActionODCreated:                        "od.created",
		ActionODOverridden:                     "od.overridden",
		ActionODRemoved:                        "od.removed",
		ActionSyncCompleted:                    "sync.completed",
		ActionOfflineEntered:                   "offline.entered",
		ActionAuthLoginSucceeded:               "auth.login_succeeded",
		ActionAuthDomainRejected:               "auth.domain_rejected",
		ActionScanBlocked:                      "scan.blocked",
		ActionDisputeSubmitted:                 "dispute.submitted",
		ActionDisputeResolved:                  "dispute.resolved",
		ActionUserProvisioned:                  "user.provisioned",
		ActionAuthSessionRevoked:               "auth.session_revoked",
		ActionEventCreated:                     "event.created",
		ActionEventArchived:                    "event.archived",
		ActionCategoryCreated:                  "category.created",
		ActionCategoryVisibilityUpdated:        "category.visibility_updated",
		ActionTeacherProfileCreated:            "teacher.profile_created",
		ActionTeacherCategoryAccessUpdated:     "teacher.category_access_updated",
		ActionStudentRosterImported:            "student_roster.imported",
		ActionCardTokenSealed:                  "card_token.sealed",
		ActionCardTokenVerified:                "card_token.verified",
		ActionCoordinatorAssigned:              "coordinator.assigned",
		ActionCoordinatorCategoryAccessUpdated: "coordinator.category_access_updated",
		ActionCaptureCreditCapChanged:          "capture_credit.cap_changed",
		ActionCaptureCreditReset:               "capture_credit.reset",
		ActionAppSettingUpdated:                "app_setting.updated",
	}

	// (1) Each constant carries its exact architecture-mandated wire value.
	for act, wireValue := range want {
		if string(act) != wireValue {
			t.Errorf("[P1] action constant has wrong wire value: got %q, want %q", string(act), wireValue)
		}
	}

	// (2) AllActions == the declared set, exactly.
	seen := make(map[Action]int, len(AllActions))
	for _, a := range AllActions {
		if a == "" {
			t.Error("[P1] AllActions contains an empty Action — the audit helper must never accept a blank action")
		}
		seen[a]++
	}
	for a, n := range seen {
		if n > 1 {
			t.Errorf("[P1] AllActions contains a duplicate entry %q (%d times)", string(a), n)
		}
		if _, ok := want[a]; !ok {
			t.Errorf("[P1] AllActions contains an unregistered extra action %q (not in the canonical set)", string(a))
		}
	}
	for a := range want {
		if seen[a] == 0 {
			t.Errorf("[P1] AllActions is MISSING declared action %q — a constant was added to events.go but left out of the SSOT slice", string(a))
		}
	}
}

// declaredActionConst matches each declared Action constant in events.go, e.g.
// `ActionODCreated Action = "od.created"`. The `Action` name prefix keeps it to
// the registry constants (not the `var AllActions = []Action{…}` entries, which
// have no ` Action = "…"`).
var declaredActionConst = regexp.MustCompile(`(?m)^\s*Action[A-Za-z0-9]+\s+Action\s*=\s*"([a-z0-9._]+)"`)

// TestAllActions_CoversEveryDeclaredConstant gives the registry REAL teeth
// independent of the hand-maintained `want` map above: it reads events.go SOURCE
// and asserts every declared Action constant is registered in AllActions. Go
// cannot reflect over declared constants, so without this a constant added to the
// const block but omitted from BOTH AllActions AND `want` would slip past
// TestAllActions_MatchesDeclaredConstants. This closes the "a story forgot to
// register its action" gap at the unit-test layer (store.WithAudit's runtime
// rejection is the second backstop).
func TestAllActions_CoversEveryDeclaredConstant(t *testing.T) {
	src, err := os.ReadFile("events.go")
	if err != nil {
		t.Fatalf("[P0] read events.go: %v", err)
	}
	matches := declaredActionConst.FindAllStringSubmatch(string(src), -1)
	if len(matches) == 0 {
		t.Fatal("[P0] parsed zero Action constants from events.go — the path or format changed; this guard is blind")
	}

	// Every declared constant must be registered in the SSOT slice.
	for _, m := range matches {
		act := Action(m[1])
		if !act.Registered() {
			t.Errorf("[P0] Action %q is declared in events.go but NOT in AllActions — register it in the SSOT slice", string(act))
		}
	}

	// Symmetric: AllActions must not carry an entry that is not a declared
	// constant (a stray wire string), so the two match exactly.
	if len(AllActions) != len(matches) {
		t.Errorf("[P0] AllActions has %d entries but events.go declares %d Action constants — they must match exactly", len(AllActions), len(matches))
	}
}
