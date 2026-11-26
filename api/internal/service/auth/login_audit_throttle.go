package auth

// login_audit_throttle.go — collapses repeated auth.login_succeeded writes for
// one Supabase session into a single audit row.
//
// WHY THIS EXISTS
//
//	/auth/me is not called once per sign-in. The web client re-resolves the
//	identity on every Supabase auth-state change — every token refresh, and every
//	time a phone comes back from the background — so one person working through a
//	morning produces dozens of calls. The original design chose a per-call cadence for the
//	login audit, and per-call against that caller means the ledger fills with rows
//	that record nothing new.
//
//	Production measured it: 2,740 InsertAuditLog calls against 319 actual
//	attendance marks. Roughly 2,157 of those audit rows — 79% of the ledger — were
//	login_succeeded for sessions already recorded. Each is a write, a WAL record,
//	and billed history storage, for a fact already in the table.
//
// WHAT IS AND IS NOT PRESERVED
//
//	Preserved: every distinct session still writes a login_succeeded row, carrying
//	the same account, role, device and session_id it always did. "Who signed in,
//	as what, from what, and when" is answerable exactly as before.
//
//	Given up: a token REFRESH within the window no longer gets its own row. The
//	session_id was already the documented dedup anchor for those, so this moves
//	the dedup from read-time to write-time rather than losing information.
//
//	NOT given up: fail-closed. A failed audit write is never remembered as a
//	success — cache.TTL does not store errors — so the next call retries and the
//	handler still refuses to answer 200 for a login it could not record.

import (
	"context"
	"time"

	"github.com/Shruhath/OD-Amrita/api/internal/cache"
)

// LoginAuditWindow bounds how long one session suppresses repeat login audits.
//
// Fifteen minutes rather than "forever per session": a bounded window keeps the
// cache from having to track session lifetimes it cannot observe, and it means a
// genuinely long-lived session still leaves a periodic trace in the ledger
// instead of one row at the start of a workday.
const LoginAuditWindow = 15 * time.Minute

// LoginAuditor is the slice of EventAuditor this decorator wraps.
type LoginAuditor interface {
	LoginSucceeded(ctx context.Context, email, role, subject, sessionID, device string) error
}

// ThrottledLoginAuditor writes at most one login_succeeded per session per
// LoginAuditWindow, delegating everything else to the wrapped auditor.
type ThrottledLoginAuditor struct {
	inner LoginAuditor
	seen  *cache.TTL[bool]
}

// NewThrottledLoginAuditor wraps an auditor with per-session suppression.
func NewThrottledLoginAuditor(inner LoginAuditor) *ThrottledLoginAuditor {
	return &ThrottledLoginAuditor{inner: inner, seen: cache.New[bool](LoginAuditWindow)}
}

// LoginSucceeded audits the login unless this session already has a row inside
// the window.
//
// The key is subject+session so two sessions of the same person are still two
// rows, and a session id reused across subjects (which must not happen, but
// costs nothing to guard) cannot suppress the wrong person's audit.
//
// An EMPTY session id is passed straight through unthrottled. The verifier
// requires session_id, so an empty one means something upstream changed; the
// safe reading is "cannot dedup", and writing an extra audit row is always the
// recoverable direction of that error.
func (t *ThrottledLoginAuditor) LoginSucceeded(ctx context.Context, email, role, subject, sessionID, device string) error {
	if sessionID == "" {
		return t.inner.LoginSucceeded(ctx, email, role, subject, sessionID, device)
	}
	_, err := t.seen.Do(ctx, subject+"|"+sessionID, func(ctx context.Context) (bool, error) {
		if err := t.inner.LoginSucceeded(ctx, email, role, subject, sessionID, device); err != nil {
			return false, err
		}
		return true, nil
	})
	return err
}
