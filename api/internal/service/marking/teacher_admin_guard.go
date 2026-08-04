package marking

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	odrecord "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

type RoleAwareMarkGuard struct {
	coordinator *MarkGuard
	queries     *sqlc.Queries
}

func NewRoleAwareMarkGuard(policy *Policy, pool *pgxpool.Pool) *RoleAwareMarkGuard {
	return &RoleAwareMarkGuard{coordinator: NewMarkGuard(policy), queries: sqlc.New(pool)}
}

func (g *RoleAwareMarkGuard) AssertCanMark(ctx context.Context, actor odrecord.Actor, eventID, categoryID string) error {
	return g.coordinator.AssertCanMark(ctx, actor, eventID, categoryID)
}

func (g *RoleAwareMarkGuard) AssertCanMarkStudent(ctx context.Context, actor odrecord.Actor, eventID, categoryID string, student odrecord.Student) error {
	switch actor.Role {
	case "admin":
		return g.assertAdmin(ctx, eventID, categoryID)
	case "teacher":
		return g.assertTeacher(ctx, actor.UserID, eventID, categoryID, student.StudentID)
	default:
		return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Teacher or admin role required."}
	}
}

func (g *RoleAwareMarkGuard) assertAdmin(ctx context.Context, eventID, categoryID string) error {
	eventUUID, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return AsDomainError(ErrEventNotFound)
	}
	categoryUUID, err := uuid.Parse(strings.TrimSpace(categoryID))
	if err != nil {
		return AsDomainError(ErrCategoryNotFound)
	}
	ok, err := g.queries.AdminCanSearchMark(ctx, sqlc.AdminCanSearchMarkParams{
		EventID:    dbUUID(eventUUID),
		CategoryID: dbUUID(categoryUUID),
	})
	if err != nil {
		return err
	}
	if !ok {
		return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Admin mark scope is not active for this event and category."}
	}
	return nil
}

func (g *RoleAwareMarkGuard) assertTeacher(ctx context.Context, userID, eventID, categoryID, studentID string) error {
	userUUID, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Teacher principal is invalid."}
	}
	eventUUID, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return AsDomainError(ErrEventNotFound)
	}
	categoryUUID, err := uuid.Parse(strings.TrimSpace(categoryID))
	if err != nil {
		return AsDomainError(ErrCategoryNotFound)
	}
	studentUUID, err := uuid.Parse(strings.TrimSpace(studentID))
	if err != nil {
		return domain.DomainError{Code: contracts.ErrorCodeSTUDENTNOTFOUND, Message: "Student not found."}
	}
	ok, err := g.queries.TeacherCanSearchMark(ctx, sqlc.TeacherCanSearchMarkParams{
		UserID:     dbUUID(userUUID),
		EventID:    dbUUID(eventUUID),
		CategoryID: dbUUID(categoryUUID),
		StudentID:  dbUUID(studentUUID),
	})
	if err != nil {
		return err
	}
	if !ok {
		return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Teacher may mark only students in a taught branch for an active event branch."}
	}
	return nil
}

func dbUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
