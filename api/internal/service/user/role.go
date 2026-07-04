// Package user owns first-login identity provisioning: mapping a verified
// Supabase identity to an application users-table row + global base role, with a
// user.provisioned audit row co-committed through store.WithAudit. It is the
// first real caller of the Story 1.4 write helper.
//
// Scope note: nothing here ENFORCES a role (RBAC/scope-checking is Epic 2) and
// nothing mints or verifies tokens (Supabase owns sessions; the auth service
// verifies them). This package only RECORDS who a verified identity is.
package user

import "strings"

// Role is the global base role recorded on a users row. It is the CLOSED set
// mirrored by the users.role CHECK constraint (0003) and the
// AuthenticatedIdentity.role contract enum. Its string value is written verbatim
// into the users.role column.
type Role string

const (
	RoleAdmin       Role = "admin"
	RoleTeacher     Role = "teacher"
	RoleCoordinator Role = "coordinator"
	RoleStudent     Role = "student"
)

// ResolveRole is the pure role-resolution rule applied at provisioning: an email
// in the bootstrap-admin allowlist resolves to admin; every other verified
// identity resolves to the LEAST-privileged student. Teacher and coordinator are
// NEVER assigned at first login — those elevations are explicit admin actions in
// Epic 2. The match is case-insensitive (the allowlist is lowercased at config
// load and the email is lowercased here too), so casing can never smuggle an
// identity past — or into — the admin gate.
func ResolveRole(email string, bootstrapAdmins map[string]struct{}) Role {
	if _, ok := bootstrapAdmins[strings.ToLower(strings.TrimSpace(email))]; ok {
		return RoleAdmin
	}
	return RoleStudent
}
