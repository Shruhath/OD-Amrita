package event

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

type fakeWriteQueries struct {
	created         sqlc.CreateEventRow
	createErr       error
	branchRows      int64
	branchErr       error
	archivedID      pgtype.UUID
	archiveErr      error
	branchWrites    int
	category        sqlc.CreateCategoryRow
	categoryErr     error
	categoryWrites  int
	categoryLastArg sqlc.CreateCategoryParams
	lockedUser      sqlc.LockCoordinatorAssignmentUserRow
	lockErr         error
	promoteRows     int64
	promoteErr      error
	promoteCalls    int
	assignment      sqlc.InsertEventCoordinatorAssignmentRow
	assignmentErr   error
	assignmentCalls int
}

func (f *fakeWriteQueries) CreateEvent(context.Context, sqlc.CreateEventParams) (sqlc.CreateEventRow, error) {
	return f.created, f.createErr
}

func (f *fakeWriteQueries) AddEventBranch(context.Context, sqlc.AddEventBranchParams) (int64, error) {
	f.branchWrites++
	return f.branchRows, f.branchErr
}

func (f *fakeWriteQueries) ArchiveEvent(context.Context, sqlc.ArchiveEventParams) (pgtype.UUID, error) {
	return f.archivedID, f.archiveErr
}

func (f *fakeWriteQueries) CreateCategory(_ context.Context, arg sqlc.CreateCategoryParams) (sqlc.CreateCategoryRow, error) {
	f.categoryWrites++
	f.categoryLastArg = arg
	return f.category, f.categoryErr
}

func (f *fakeWriteQueries) LockCoordinatorAssignmentUser(context.Context, pgtype.UUID) (sqlc.LockCoordinatorAssignmentUserRow, error) {
	return f.lockedUser, f.lockErr
}

func (f *fakeWriteQueries) PromoteUserToCoordinator(context.Context, pgtype.UUID) (int64, error) {
	f.promoteCalls++
	return f.promoteRows, f.promoteErr
}

func (f *fakeWriteQueries) InsertEventCoordinatorAssignment(context.Context, sqlc.InsertEventCoordinatorAssignmentParams) (sqlc.InsertEventCoordinatorAssignmentRow, error) {
	f.assignmentCalls++
	return f.assignment, f.assignmentErr
}

func (f *fakeWriteQueries) AssignUserCategory(context.Context, sqlc.AssignUserCategoryParams) (sqlc.UserCategoryAssignment, error) {
	return sqlc.UserCategoryAssignment{}, nil
}

func (f *fakeWriteQueries) SetCategoryPublicVisibility(context.Context, sqlc.SetCategoryPublicVisibilityParams) (sqlc.SetCategoryPublicVisibilityRow, error) {
	return sqlc.SetCategoryPublicVisibilityRow{}, nil
}

func (f *fakeWriteQueries) UpsertCaptureCreditConfig(context.Context, sqlc.UpsertCaptureCreditConfigParams) (sqlc.UpsertCaptureCreditConfigRow, error) {
	return sqlc.UpsertCaptureCreditConfigRow{}, nil
}

func (f *fakeWriteQueries) ResetCaptureCreditUsage(context.Context, sqlc.ResetCaptureCreditUsageParams) (sqlc.ResetCaptureCreditUsageRow, error) {
	return sqlc.ResetCaptureCreditUsageRow{}, nil
}

// TestSQLCWriterCreateCategoryReturnsTheRowTheDatabaseWrote pins that the id and
// timestamp come from RETURNING rather than being synthesized client-side: a
// synthesized id would name a row the ledger could never be joined to.
func TestSQLCWriterCreateCategoryReturnsTheRowTheDatabaseWrote(t *testing.T) {
	categoryID := uuid.New()
	eventID := uuid.New()
	createdAt := time.Date(2026, 7, 21, 3, 0, 0, 0, time.UTC)
	queries := &fakeWriteQueries{category: sqlc.CreateCategoryRow{
		ID:        dbUUID(categoryID),
		EventID:   dbUUID(eventID),
		Name:      "Procession",
		CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true},
	}}
	writer := newSQLCWriter(func(pgx.Tx) eventWriteQueries { return queries })

	got, changed, err := writer.CreateCategory(context.Background(), nil, CreateCategoryWrite{
		EventID: eventID, Name: "Procession",
	})
	if err != nil || !changed {
		t.Fatalf("CreateCategory = (%#v, %v, %v), want the inserted row", got, changed, err)
	}
	if got.ID != categoryID.String() || got.EventID != eventID.String() || !got.CreatedAt.Equal(createdAt) {
		t.Errorf("category = %#v, want the RETURNING values", got)
	}
	if queries.categoryLastArg.Name != "Procession" {
		t.Errorf("persisted name = %q, want the normalized name", queries.categoryLastArg.Name)
	}
}

func TestSQLCWriterCreateCategorySurfacesTheDatabaseError(t *testing.T) {
	// The 23505/55000 mapping lives in the service; the writer must hand the
	// driver error through unwrapped so errors.As can still reach *pgconn.PgError.
	boom := errors.New("insert failed")
	queries := &fakeWriteQueries{categoryErr: boom}
	writer := newSQLCWriter(func(pgx.Tx) eventWriteQueries { return queries })

	_, changed, err := writer.CreateCategory(context.Background(), nil, CreateCategoryWrite{
		EventID: uuid.New(), Name: "Procession",
	})
	if !errors.Is(err, boom) || changed {
		t.Fatalf("CreateCategory = (%v, %v), want the raw error and changed=false", changed, err)
	}
}

func TestSQLCWriterCreateCoordinatorAssignmentPromotesOnlyStudents(t *testing.T) {
	userID := uuid.New()
	eventID := uuid.New()
	actorID := uuid.New()
	assignmentID := uuid.New()
	createdAt := time.Date(2026, 8, 3, 4, 30, 0, 0, time.UTC)

	for _, tc := range []struct {
		name         string
		role         string
		promoteRows  int64
		wantPromotes int
	}{
		{name: "student is promoted", role: "student", promoteRows: 1, wantPromotes: 1},
		{name: "coordinator is not re-promoted", role: "coordinator", promoteRows: 0, wantPromotes: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries := &fakeWriteQueries{
				lockedUser: sqlc.LockCoordinatorAssignmentUserRow{
					ID: dbUUID(userID), Email: "coord@amrita.edu", Role: tc.role,
				},
				promoteRows: tc.promoteRows,
				assignment: sqlc.InsertEventCoordinatorAssignmentRow{
					ID: dbUUID(assignmentID), UserID: dbUUID(userID), EventID: dbUUID(eventID),
					CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true},
				},
			}
			writer := newSQLCWriter(func(pgx.Tx) eventWriteQueries { return queries })

			got, changed, err := writer.CreateCoordinatorAssignment(context.Background(), nil, CreateCoordinatorAssignmentWrite{
				UserID: userID, EventID: eventID, CreatedByUserID: actorID,
			})
			if err != nil || !changed {
				t.Fatalf("CreateCoordinatorAssignment = (%#v, %v, %v)", got, changed, err)
			}
			if queries.promoteCalls != tc.wantPromotes {
				t.Fatalf("promote calls = %d, want %d", queries.promoteCalls, tc.wantPromotes)
			}
			if queries.assignmentCalls != 1 {
				t.Fatalf("assignment insert calls = %d, want 1", queries.assignmentCalls)
			}
			if got.ID != assignmentID.String() || got.UserID != userID.String() || got.Email != "coord@amrita.edu" {
				t.Fatalf("assignment = %#v, want database row plus locked user email", got)
			}
		})
	}
}

func TestSQLCWriterCreateCoordinatorAssignmentRejectsIneligibleLockedRole(t *testing.T) {
	queries := &fakeWriteQueries{lockedUser: sqlc.LockCoordinatorAssignmentUserRow{
		ID: dbUUID(uuid.New()), Email: "teacher@amrita.edu", Role: "teacher",
	}}
	writer := newSQLCWriter(func(pgx.Tx) eventWriteQueries { return queries })
	_, changed, err := writer.CreateCoordinatorAssignment(context.Background(), nil, CreateCoordinatorAssignmentWrite{
		UserID: uuid.New(), EventID: uuid.New(), CreatedByUserID: uuid.New(),
	})
	if !errors.Is(err, ErrCoordinatorRoleAlreadyAssigned) || changed {
		t.Fatalf("CreateCoordinatorAssignment = (%v, %v), want ErrCoordinatorRoleAlreadyAssigned and changed=false", changed, err)
	}
	if queries.assignmentCalls != 0 {
		t.Fatalf("assignment insert reached for ineligible role")
	}
}

func TestSQLCWriterCreateUsesReturningAndEveryAssociation(t *testing.T) {
	eventID := uuid.New()
	queries := &fakeWriteQueries{
		created:    sqlc.CreateEventRow{ID: dbUUID(eventID)},
		branchRows: 1,
	}
	writer := newSQLCWriter(func(pgx.Tx) eventWriteQueries { return queries })

	got, changed, err := writer.Create(context.Background(), nil, CreateWrite{
		Name:      "Innovation Day",
		EventDate: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
		BranchIDs: []uuid.UUID{uuid.New(), uuid.New()},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if !changed || got != eventID {
		t.Fatalf("Create = (%s, %v), want (%s, true)", got, changed, eventID)
	}
	if queries.branchWrites != 2 {
		t.Errorf("branch writes = %d, want 2", queries.branchWrites)
	}
}

func TestSQLCWriterCreateRejectsAssociationNoOp(t *testing.T) {
	queries := &fakeWriteQueries{
		created:    sqlc.CreateEventRow{ID: dbUUID(uuid.New())},
		branchRows: 0,
	}
	writer := newSQLCWriter(func(pgx.Tx) eventWriteQueries { return queries })

	_, _, err := writer.Create(context.Background(), nil, CreateWrite{
		Name:      "Innovation Day",
		EventDate: time.Now(),
		EndDate:   time.Now(),
		BranchIDs: []uuid.UUID{uuid.New()},
	})
	if err == nil {
		t.Fatal("Create succeeded when an association affected zero rows")
	}
}

func TestSQLCWriterArchiveChangedOnlyForConditionalReturning(t *testing.T) {
	eventID := uuid.New()
	actorID := uuid.New()

	for _, tc := range []struct {
		name    string
		result  pgtype.UUID
		err     error
		changed bool
	}{
		{name: "transition", result: dbUUID(eventID), changed: true},
		{name: "idempotent no-op", err: pgx.ErrNoRows, changed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries := &fakeWriteQueries{archivedID: tc.result, archiveErr: tc.err}
			writer := newSQLCWriter(func(pgx.Tx) eventWriteQueries { return queries })
			changed, err := writer.Archive(context.Background(), nil, eventID, actorID)
			if err != nil {
				t.Fatalf("Archive returned error: %v", err)
			}
			if changed != tc.changed {
				t.Errorf("changed = %v, want %v", changed, tc.changed)
			}
		})
	}
}

func TestSQLCWriterArchivePropagatesDatabaseError(t *testing.T) {
	boom := errors.New("database failed")
	queries := &fakeWriteQueries{archiveErr: boom}
	writer := newSQLCWriter(func(pgx.Tx) eventWriteQueries { return queries })
	_, err := writer.Archive(context.Background(), nil, uuid.New(), uuid.New())
	if !errors.Is(err, boom) {
		t.Fatalf("Archive error = %v, want %v", err, boom)
	}
}
