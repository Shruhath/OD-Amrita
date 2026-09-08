package odrecord

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

type batchTestWriter struct {
	existing []Student
	records  []RecordInput
}

func (w *batchTestWriter) InsertPresent(_ context.Context, _ pgx.Tx, input RecordInput) (MarkResult, error) {
	w.records = append(w.records, input)
	return MarkResult{RecordID: input.StudentID, Student: input.Student, EventID: input.EventID, CategoryID: input.CategoryID, Slot: input.Slot, ODDate: input.ODDate, CaptureMethod: input.CaptureMethod, ReasonNote: input.ReasonNote, ServerReceivedAt: input.ServerReceivedAt}, nil
}

func (w *batchTestWriter) ExistingPresentForBatch(context.Context, pgx.Tx, BatchMarkInput, time.Time) ([]Student, error) {
	return w.existing, nil
}

type batchAuditStore struct{ actions []domain.Action }

func (s *batchAuditStore) WithAudit(ctx context.Context, event store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error {
	changed, err := write(ctx, nil)
	if err != nil {
		return err
	}
	if changed {
		s.actions = append(s.actions, event.Action)
	}
	return nil
}
func (s *batchAuditStore) Audit(_ context.Context, _ pgx.Tx, event store.AuditEvent) error {
	s.actions = append(s.actions, event.Action)
	return nil
}

type batchReader struct{ students map[string]Student }

func (r batchReader) EventForMark(context.Context, string) (Event, error) {
	day := time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC)
	return Event{EventID: scaffoldEventID, EventDate: day, EndDate: day, Status: "active"}, nil
}
func (r batchReader) CategoryForEvent(context.Context, string, string) (Category, error) {
	return Category{}, nil
}
func (r batchReader) StudentForMark(_ context.Context, id string) (Student, error) {
	return r.students[id], nil
}

func TestMarkBatch_AtomicSuccessCreatesOneAuditPerRecord(t *testing.T) {
	studentA, studentB := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	writer, audits := &batchTestWriter{}, &batchAuditStore{}
	students := map[string]Student{studentA: {StudentID: studentA, RollNo: "A", DisplayName: "A"}, studentB: {StudentID: studentB, RollNo: "B", DisplayName: "B"}}
	svc := NewService(batchReader{students: students}, stubCardResolver(), writer, audits, scaffoldClock{}, branchScopingGuard{}, stubCreditLedger())
	day := campusTime(t, 2026, time.August, 3, 0, 0)
	results, err := svc.MarkBatch(context.Background(), Actor{UserID: scaffoldActorID, Email: "admin@amrita.edu", Role: "admin"}, BatchMarkInput{EventID: scaffoldEventID, CategoryID: scaffoldCategoryID, StudentIDs: []string{studentA, studentB}, Slot: 4, ODDate: day, ReasonNote: "manual group attendance"})
	if err != nil {
		t.Fatalf("MarkBatch = %v", err)
	}
	if len(results) != 2 || len(writer.records) != 2 || len(audits.actions) != 2 {
		t.Fatalf("results/writes/audits = %d/%d/%d, want 2/2/2", len(results), len(writer.records), len(audits.actions))
	}
}

func TestMarkBatch_DuplicatePreflightWritesNothing(t *testing.T) {
	studentID := "11111111-1111-4111-8111-111111111111"
	writer, audits := &batchTestWriter{existing: []Student{{StudentID: studentID, RollNo: "A", DisplayName: "Already marked"}}}, &batchAuditStore{}
	svc := NewService(batchReader{students: map[string]Student{studentID: {StudentID: studentID}}}, stubCardResolver(), writer, audits, scaffoldClock{}, branchScopingGuard{}, stubCreditLedger())
	_, err := svc.MarkBatch(context.Background(), Actor{UserID: scaffoldActorID, Email: "admin@amrita.edu", Role: "admin"}, BatchMarkInput{EventID: scaffoldEventID, CategoryID: scaffoldCategoryID, StudentIDs: []string{studentID}, Slot: 4, ODDate: campusTime(t, 2026, time.August, 3, 0, 0), ReasonNote: "manual group attendance"})
	if _, ok := err.(BatchDuplicateError); !ok {
		t.Fatalf("MarkBatch error = %v, want BatchDuplicateError", err)
	}
	if len(writer.records) != 0 || len(audits.actions) != 0 {
		t.Fatalf("writes/audits = %d/%d, want 0/0", len(writer.records), len(audits.actions))
	}
}
