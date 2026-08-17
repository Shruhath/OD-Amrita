package student

// writer_test.go — the roster import's row accounting.
//
// The import used to run one upsert per student in a Go loop, which made the
// counting trivial: each iteration knew which row it was on, and pgx.ErrNoRows
// meant "this one was unchanged". It is now ONE statement for the whole file, so
// the same three numbers have to be reconstructed from what came back — and the
// upsert's IS DISTINCT FROM guard means an unchanged row returns NOTHING, not a
// row saying so.
//
// These tests exist because that reconstruction is the part a reviewer cannot
// check by eye, and because the numbers are shown to an administrator who has
// just uploaded a file and is deciding whether it worked.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

// fakeWriteQueries returns canned upsert results and records what it was asked.
type fakeWriteQueries struct {
	returned []sqlc.UpsertStudentsBulkRow
	err      error
	calls    int
	params   sqlc.UpsertStudentsBulkParams
	count    int32
}

func (f *fakeWriteQueries) UpsertStudentsBulk(_ context.Context, arg sqlc.UpsertStudentsBulkParams) ([]sqlc.UpsertStudentsBulkRow, error) {
	f.calls++
	f.params = arg
	return f.returned, f.err
}

func (f *fakeWriteQueries) CountStudents(context.Context) (int32, error) { return f.count, nil }

func (f *fakeWriteQueries) InsertStudentRosterImport(context.Context, sqlc.InsertStudentRosterImportParams) (pgtype.UUID, error) {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}, nil
}

func validID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

func importRows(n int) []ImportRow {
	branch := uuid.NewString()
	rows := make([]ImportRow, 0, n)
	for i := range n {
		rows = append(rows, ImportRow{
			RollNo:   "AM" + string(rune('A'+i)),
			Name:     "Person",
			BranchID: branch,
			Section:  "A",
			Batch:    "2026",
		})
	}
	return rows
}

func runImport(t *testing.T, q *fakeWriteQueries, rows []ImportRow) Result {
	t.Helper()
	writer := newSQLCWriter(func(pgx.Tx) studentWriteQueries { return q })
	result, err := writer.Import(context.Background(), nil, ImportWrite{
		Rows:      rows,
		TotalRows: len(rows),
		Actor:     Actor{UserID: uuid.NewString(), Email: "admin@amrita.edu"},
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return result
}

// THE WHOLE FILE IS ONE ROUND TRIP. This is the property the change exists for:
// 2,000 students used to be 2,000 sequential statements inside one transaction.
func TestImport_SendsEveryRowInASingleStatement(t *testing.T) {
	rows := importRows(5)
	q := &fakeWriteQueries{}
	for i := range rows {
		q.returned = append(q.returned, sqlc.UpsertStudentsBulkRow{
			Ordinality: int64(i + 1), ID: validID(), Inserted: true,
		})
	}

	runImport(t, q, rows)

	if q.calls != 1 {
		t.Errorf("issued %d upsert statements for 5 rows, want 1", q.calls)
	}
	if got := len(q.params.RollNos); got != 5 {
		t.Errorf("sent %d roll numbers, want 5", got)
	}
	// The five arrays are positional — position i of each is one student — so a
	// length mismatch would pair a name with the wrong roll number.
	if len(q.params.Names) != 5 || len(q.params.BranchIds) != 5 ||
		len(q.params.Sections) != 5 || len(q.params.Batches) != 5 {
		t.Errorf("column arrays have mismatched lengths: rolls=%d names=%d branches=%d sections=%d batches=%d",
			len(q.params.RollNos), len(q.params.Names), len(q.params.BranchIds),
			len(q.params.Sections), len(q.params.Batches))
	}
}

// An UNCHANGED row returns nothing at all, so it can only be counted as a gap.
func TestImport_CountsUnchangedRowsFromWhatDidNotComeBack(t *testing.T) {
	rows := importRows(6)
	q := &fakeWriteQueries{returned: []sqlc.UpsertStudentsBulkRow{
		{Ordinality: 1, ID: validID(), Inserted: true},
		{Ordinality: 3, ID: validID(), Inserted: false},
		{Ordinality: 4, ID: validID(), Inserted: true},
	}}

	result := runImport(t, q, rows)

	if result.InsertedRows != 2 {
		t.Errorf("InsertedRows = %d, want 2", result.InsertedRows)
	}
	if result.UpdatedRows != 1 {
		t.Errorf("UpdatedRows = %d, want 1", result.UpdatedRows)
	}
	if result.UnchangedRows != 3 {
		t.Errorf("UnchangedRows = %d, want 3 — rows 2, 5 and 6 were not returned, so nothing changed for them", result.UnchangedRows)
	}
	if result.TotalRows != 6 {
		t.Errorf("TotalRows = %d, want 6", result.TotalRows)
	}
	// The three counts must account for every row, or the summary an
	// administrator reads after an upload does not add up.
	if sum := result.InsertedRows + result.UpdatedRows + result.UnchangedRows; sum != result.TotalRows {
		t.Errorf("counts sum to %d but TotalRows is %d", sum, result.TotalRows)
	}
}

// Re-importing an identical file changes nothing and must say so, rather than
// reading as a failed import.
func TestImport_IdenticalReimportIsAllUnchanged(t *testing.T) {
	rows := importRows(4)
	result := runImport(t, &fakeWriteQueries{returned: nil}, rows)

	if result.UnchangedRows != 4 {
		t.Errorf("UnchangedRows = %d, want 4", result.UnchangedRows)
	}
	if result.InsertedRows != 0 || result.UpdatedRows != 0 {
		t.Errorf("Inserted = %d, Updated = %d, want 0 and 0", result.InsertedRows, result.UpdatedRows)
	}
}

// pgx.ErrNoRows from a :many query means the same thing as an empty slice here,
// and the loop this replaced treated it that way.
func TestImport_TreatsErrNoRowsAsNothingChanged(t *testing.T) {
	rows := importRows(3)
	result := runImport(t, &fakeWriteQueries{err: pgx.ErrNoRows}, rows)

	if result.UnchangedRows != 3 {
		t.Errorf("UnchangedRows = %d, want 3", result.UnchangedRows)
	}
}

// An empty file must not issue a statement at all: unnest over empty arrays is
// legal but pointless, and the guard keeps the no-op honest.
func TestImport_EmptyFileIssuesNoUpsert(t *testing.T) {
	q := &fakeWriteQueries{}
	result := runImport(t, q, nil)

	if q.calls != 0 {
		t.Errorf("issued %d statements for an empty file, want 0", q.calls)
	}
	if result.InsertedRows != 0 || result.UpdatedRows != 0 || result.UnchangedRows != 0 {
		t.Errorf("counts must all be zero, got %+v", result)
	}
}

// A real failure must surface, not be mistaken for "nothing changed".
func TestImport_SurfacesWriteFailures(t *testing.T) {
	writer := newSQLCWriter(func(pgx.Tx) studentWriteQueries {
		return &fakeWriteQueries{err: errors.New("deadlock detected")}
	})
	_, err := writer.Import(context.Background(), nil, ImportWrite{
		Rows: importRows(2), TotalRows: 2, Actor: Actor{UserID: uuid.NewString()},
	})
	if err == nil {
		t.Fatal("want the write failure surfaced")
	}
}

// A malformed branch id must be caught before anything is sent.
func TestImport_RejectsAnInvalidBranchIDBeforeWriting(t *testing.T) {
	rows := importRows(2)
	rows[1].BranchID = "not-a-uuid"
	q := &fakeWriteQueries{}

	writer := newSQLCWriter(func(pgx.Tx) studentWriteQueries { return q })
	if _, err := writer.Import(context.Background(), nil, ImportWrite{
		Rows: rows, TotalRows: 2, Actor: Actor{UserID: uuid.NewString()},
	}); err == nil {
		t.Fatal("want an error for a malformed branch id")
	}
	if q.calls != 0 {
		t.Errorf("issued %d statements despite a malformed row, want 0", q.calls)
	}
}
