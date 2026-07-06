package auth

// supabase_revoker.go — the best-effort Supabase-side revocation seam (Story
// 1.5c, DoD 5/11).
//
// WHY THIS IS DISABLED BY DEFAULT. Supabase/GoTrue exposes NO supported
// server-side API to force-log-out ANOTHER user's sessions by subject: the admin
// sign-out (`POST {authUrl}/logout?scope=`) requires the TARGET user's own valid
// JWT, which a forced-logout admin does not hold. The story forbids inventing a
// workaround (e.g. storing users' refresh/access tokens to replay their logout).
// So the production wiring uses DisabledRevoker: the LOCAL deny-list
// (revocation_checker.go) is authoritative for our API's next-request guarantee,
// and Supabase access simply lapses at token expiry. If a supported target-user
// revoke API is confirmed later, add an adapter here that implements
// SupabaseRevoker without persisting any token; the service already treats it as
// best-effort (its failure never blocks the local revoke).
//
// See docs/auth-microsoft-supabase-setup.md for the runbook note.

import "context"

// DisabledRevoker is the no-op SupabaseRevoker used when no supported server-side
// target-user revocation is available. It always succeeds (nothing to do); the
// local deny-list carries the enforcement.
type DisabledRevoker struct{}

func (DisabledRevoker) RevokeSessions(context.Context, string, *string, RevokeScope) error {
	return nil
}
