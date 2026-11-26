package userrole

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

// SQLCWriter runs a demotion with the sqlc queries in db/queries/user_roles.sql.
type SQLCWriter struct{}

func NewSQLCWriter() SQLCWriter { return SQLCWriter{} }

// Demote runs every step on tx. The ORDER is load-bearing:
//
//  1. lock every admin row (id order) BEFORE the target. Locking the target
//     first would let two admins demoting each other each hold one row and wait
//     for the other's — a deadlock. Taking the admin set first serialises them;
//     the second then re-reads and sees who is still an admin.
//  2. lock the target, and stop (no-op) if it is already a student.
//  3. refuse if the target is the last admin, or the actor no longer is one.
//  4. lock the active coordinator assignments, so a concurrent credit debit
//     waits and then fails its FK check instead of racing the deletes.
//  5. snapshot the credit counters, then delete the assignments (the counters
//     go with them by ON DELETE CASCADE, 0027), category access and branches;
//     set the role.
//  6. record the scope=all revocation.
func (SQLCWriter) Demote(ctx context.Context, tx pgx.Tx, input DemoteWrite) (Result, error) {
	q := sqlc.New(tx)
	target := dbUUID(input.TargetID)

	admins, err := q.LockAdminUsers(ctx)
	if err != nil {
		return Result{}, err
	}
	user, err := q.LockUserForRoleChange(ctx, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, ErrUserNotFound
	}
	if err != nil {
		return Result{}, err
	}
	result := Result{
		UserID:       input.TargetID.String(),
		Subject:      user.SupabaseSubject,
		Email:        user.Email,
		PreviousRole: user.Role,
		Role:         RoleStudent,
	}
	if user.Role == RoleStudent {
		return result, nil
	}

	actorIsAdmin := false
	otherAdmins := 0
	for _, id := range admins {
		switch uuid.UUID(id.Bytes) {
		case input.ActorID:
			actorIsAdmin = true
			otherAdmins++
		case input.TargetID:
		default:
			otherAdmins++
		}
	}
	if !actorIsAdmin {
		return Result{}, ErrActorNotAdmin
	}
	if user.Role == RoleAdmin && otherAdmins == 0 {
		return Result{}, ErrLastAdmin
	}

	if _, err := q.LockActiveCoordinatorAssignmentsForUser(ctx, target); err != nil {
		return Result{}, err
	}
	usage, err := q.SumActiveCaptureCreditUsageForUser(ctx, target)
	if err != nil {
		return Result{}, err
	}
	eventIDs, err := q.DeleteActiveCoordinatorAssignmentsForUser(ctx, target)
	if err != nil {
		return Result{}, err
	}
	categories, err := q.DeleteAllUserCategoryAssignments(ctx, target)
	if err != nil {
		return Result{}, err
	}
	branches, err := q.DeleteUserBranches(ctx, target)
	if err != nil {
		return Result{}, err
	}
	demoted, err := q.DemoteUserToStudent(ctx, target)
	if err != nil {
		return Result{}, err
	}
	if demoted != 1 {
		return Result{}, fmt.Errorf("userrole: demotion updated %d user rows, want 1", demoted)
	}
	revocation, err := q.InsertRevocation(ctx, sqlc.InsertRevocationParams{
		SupabaseSubject: user.SupabaseSubject,
		Scope:           "all",
		Reason:          RevocationReason,
		RevokedByUserID: dbUUID(input.ActorID),
	})
	if err != nil {
		return Result{}, err
	}

	result.Changed = true
	result.RemovedCoordinatorEventIDs = make([]string, 0, len(eventIDs))
	for _, id := range eventIDs {
		if id.Valid {
			result.RemovedCoordinatorEventIDs = append(result.RemovedCoordinatorEventIDs, uuid.UUID(id.Bytes).String())
		}
	}
	result.RemovedCreditUsageRows = usage.Rows
	result.RemovedCreditUsageConsumed = usage.Consumed
	result.RemovedCategoryAssignments = categories
	result.RemovedTeacherBranches = branches
	result.RevocationID = revocation.ID
	result.SessionsRevoked = true
	return result, nil
}

func dbUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
