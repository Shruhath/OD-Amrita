package domain

// events.go ★ — the audit-event registry.
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
// source of truth. Registration discipline is a CONVENTIONS rule: every change
// that introduces a new auditable action MUST add its constant here AND to
// AllActions. events_test.go fails if a declared constant is missing from
// AllActions, and store.WithAudit rejects an unregistered action at runtime — so
// "a change forgot to register its action" fails loudly, never silently.
type Action string

// The domain.action constants. Only the actions the current surface needs are
// declared; each new feature adds its own (scan-path names, …) when it needs
// them — do not pre-add future names.
const (
	ActionODCreated          Action = "od.created"
	ActionODOverridden       Action = "od.overridden"
	ActionODRemoved          Action = "od.removed"
	ActionSyncCompleted      Action = "sync.completed"
	ActionOfflineEntered     Action = "offline.entered"
	ActionAuthLoginSucceeded Action = "auth.login_succeeded"
	ActionAuthDomainRejected Action = "auth.domain_rejected"
	ActionScanBlocked        Action = "scan.blocked"
	ActionDisputeSubmitted   Action = "dispute.submitted"
	ActionDisputeResolved    Action = "dispute.resolved"
	ActionUserProvisioned    Action = "user.provisioned"
	// ActionAuthSessionRevoked is the SETTLED forced-logout / session-revocation
	// event. The broader auth-event audit family MUST reuse this name — it must
	// not add a duplicate forced-logout event.
	ActionAuthSessionRevoked Action = "auth.session_revoked"
	ActionEventCreated       Action = "event.created"
	ActionEventArchived      Action = "event.archived"
	// ActionCategoryCreated is the per-event category creation event.
	// Singular entity, past-tense verb — the string is written verbatim into the
	// immutable ledger, so it is a permanent wire commitment.
	ActionCategoryCreated           Action = "category.created"
	ActionCategoryVisibilityUpdated Action = "category.visibility_updated"
	// ActionTeacherProfileCreated covers a role elevation AND every branch
	// association from the same request as ONE audit row — mirroring
	// event.created's single row covering the event insert and every
	// event_branches insert.
	ActionTeacherProfileCreated            Action = "teacher.profile_created"
	ActionTeacherCategoryAccessUpdated     Action = "teacher.category_access_updated"
	ActionStudentRosterImported            Action = "student_roster.imported"
	ActionCardTokenSealed                  Action = "card_token.sealed"
	ActionCardTokenVerified                Action = "card_token.verified"
	ActionCoordinatorAssigned              Action = "coordinator.assigned"
	ActionCoordinatorCategoryAccessUpdated Action = "coordinator.category_access_updated"
	ActionCaptureCreditCapChanged          Action = "capture_credit.cap_changed"
	ActionCaptureCreditReset               Action = "capture_credit.reset"
	ActionAppSettingUpdated                Action = "app_setting.updated"
	// ActionCoordinatorRemoved is one coordinator taken off one event: the
	// assignment, its category access on that event and its fallback-credit
	// counters, deleted together.
	ActionCoordinatorRemoved Action = "coordinator.removed"
	// ActionUserDemoted is a staff role taken away (back to student), with every
	// access row the role carried and the forced logout that makes it immediate.
	ActionUserDemoted Action = "user.demoted"
)

// AllActions is the canonical registry-of-record: every declared Action, in
// declaration order. It is the closed set store.WithAudit validates against.
// Declaring a constant above without adding it here is caught by events_test.go.
var AllActions = []Action{
	ActionODCreated,
	ActionODOverridden,
	ActionODRemoved,
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
	ActionCategoryVisibilityUpdated,
	ActionTeacherProfileCreated,
	ActionTeacherCategoryAccessUpdated,
	ActionStudentRosterImported,
	ActionCardTokenSealed,
	ActionCardTokenVerified,
	ActionCoordinatorAssigned,
	ActionCoordinatorCategoryAccessUpdated,
	ActionCaptureCreditCapChanged,
	ActionCaptureCreditReset,
	ActionAppSettingUpdated,
	ActionCoordinatorRemoved,
	ActionUserDemoted,
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
