package domain

// events.go ★ — the audit-event registry (Story 1.4, AC 5).
//
// Action is the typed name of an auditable, state-changing action. Its string
// value is the domain.action wire name written verbatim into the immutable
// audit ledger (architecture#Communication-Patterns): past-tense, dotted, e.g.
// "od.created". There are NO free-text audit events anywhere — the
// same-transaction write helper (store.WithAudit) accepts only an Action and
// rejects any value not registered in AllActions.
//
// SSOT: audit events are NOT part of the OpenAPI contract (unlike error codes,
// whose SSOT is the generated Error.code enum), so THIS FILE is their single
// source of truth. Registration discipline is a CONVENTIONS rule: every story
// that introduces a new auditable action MUST add its constant here AND to
// AllActions. events_test.go fails if a declared constant is missing from
// AllActions, and store.WithAudit rejects an unregistered action at runtime — so
// "a story forgot to register its action" fails loudly, never silently.
type Action string

// The domain.action constants, grounded in architecture#Communication-Patterns
// (lines 472-475). Only the actions Epic 1's surface needs are declared now;
// each future story adds its own (session.revoked, scan-path names, …) when it
// needs them — do not pre-add future names.
const (
	ActionODCreated          Action = "od.created"
	ActionODOverridden       Action = "od.overridden"
	ActionSyncCompleted      Action = "sync.completed"
	ActionOfflineEntered     Action = "offline.entered"
	ActionAuthLoginSucceeded Action = "auth.login_succeeded"
	ActionAuthDomainRejected Action = "auth.domain_rejected"
	ActionScanBlocked        Action = "scan.blocked"
	ActionDisputeSubmitted   Action = "dispute.submitted"
	ActionDisputeResolved    Action = "dispute.resolved"
	ActionUserProvisioned    Action = "user.provisioned"
	// ActionAuthSessionRevoked is the SETTLED forced-logout / session-revocation
	// event (Story 1.5c). Story 1.7 owns the broader auth-event audit family and
	// MUST reuse this name — it must not add a duplicate forced-logout event.
	ActionAuthSessionRevoked Action = "auth.session_revoked"
	ActionEventCreated       Action = "event.created"
	ActionEventArchived      Action = "event.archived"
	// ActionCategoryCreated is the per-event category creation event (Story 2.2).
	// Singular entity, past-tense verb — the string is written verbatim into the
	// immutable ledger, so it is a permanent wire commitment.
	ActionCategoryCreated Action = "category.created"
	// ActionTeacherProfileCreated covers a role elevation AND every branch
	// association from the same request as ONE audit row (Story 2.3) — mirroring
	// event.created's single row covering the event insert and every
	// event_branches insert.
	ActionTeacherProfileCreated   Action = "teacher.profile_created"
	ActionStudentRosterImported   Action = "student_roster.imported"
	ActionCardTokenSealed         Action = "card_token.sealed"
	ActionCoordinatorAssigned     Action = "coordinator.assigned"
	ActionCaptureCreditCapChanged Action = "capture_credit.cap_changed"
	ActionCaptureCreditReset      Action = "capture_credit.reset"
)

// AllActions is the canonical registry-of-record: every declared Action, in
// declaration order. It is the closed set store.WithAudit validates against.
// Declaring a constant above without adding it here is caught by events_test.go.
var AllActions = []Action{
	ActionODCreated,
	ActionODOverridden,
	ActionSyncCompleted,
	ActionOfflineEntered,
	ActionAuthLoginSucceeded,
	ActionAuthDomainRejected,
	ActionScanBlocked,
	ActionDisputeSubmitted,
	ActionDisputeResolved,
	ActionUserProvisioned,
	ActionAuthSessionRevoked,
	ActionEventCreated,
	ActionEventArchived,
	ActionCategoryCreated,
	ActionTeacherProfileCreated,
	ActionStudentRosterImported,
	ActionCardTokenSealed,
	ActionCoordinatorAssigned,
	ActionCaptureCreditCapChanged,
	ActionCaptureCreditReset,
}

// registeredActions is the O(1) lookup set DERIVED from AllActions, so it can
// never drift from the SSOT slice (never hand-maintained separately).
var registeredActions = func() map[Action]struct{} {
	m := make(map[Action]struct{}, len(AllActions))
	for _, a := range AllActions {
		m[a] = struct{}{}
	}
	return m
}()

// Registered reports whether a is a known auditable action. The empty Action is
// never registered, so a blank action is rejected too. store.WithAudit calls
// this to reject empty/unregistered actions before writing any audit row.
func (a Action) Registered() bool {
	_, ok := registeredActions[a]
	return ok
}
