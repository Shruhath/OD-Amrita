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
	created      sqlc.CreateEventRow
	createErr    error
	branchRows   int64
	branchErr    error
	archivedID   pgtype.UUID
	archiveErr   error
	branchWrites int
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
