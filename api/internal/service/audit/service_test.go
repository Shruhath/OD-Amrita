package audit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

func TestList_NormalizesFiltersAndPaginatesNewestFirst(t *testing.T) {
	created := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	id := uuid.MustParse("97000000-0000-4000-8000-000000000001")
	queries := &fakeQueries{
		count: 7,
		rows: []sqlc.ListAuditLogRow{{
			ID:           pgtype.UUID{Bytes: id, Valid: true},
			Action:       "od.created",
			ActorAccount: text("admin@amrita.edu"),
			ActorRole:    text("admin"),
			TargetTable:  text("od_records"),
			TargetID:     text("record-1"),
			CreatedAt:    pgtype.Timestamptz{Time: created, Valid: true},
			Payload:      mustJSON(t, map[string]any{"slot": 2}),
		}},
	}

	page, err := NewServiceWithQueries(queries).List(context.Background(), Actor{Role: "admin"}, ListInput{
		Action:       " od.created ",
		ActorAccount: " ADMIN@AMRITA.EDU ",
		TargetTable:  " od_records ",
		TargetID:     " record-1 ",
		Page:         2,
		PageSize:     3,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if queries.countParams.ActorAccount != "admin@amrita.edu" || queries.countParams.Action != "od.created" {
		t.Fatalf("count filters = %#v, want trimmed action and lowercased actor", queries.countParams)
	}
	if queries.listParams.RowOffset != 3 || queries.listParams.RowLimit != 3 {
		t.Fatalf("list offset/limit = %d/%d, want 3/3", queries.listParams.RowOffset, queries.listParams.RowLimit)
	}
	if page.Total != 7 || page.Page != 2 || page.PageSize != 3 || len(page.Data) != 1 {
		t.Fatalf("page = %+v, want total=7 page=2 page_size=3 one row", page)
	}
	entry := page.Data[0]
	if entry.ID != id.String() || entry.TargetTable == nil || *entry.TargetTable != "od_records" {
		t.Fatalf("entry target/id = %+v", entry)
	}
	if entry.Payload["slot"] != float64(2) {
		t.Fatalf("payload = %#v, want decoded slot", entry.Payload)
	}
}

func TestList_DefaultsAndValidation(t *testing.T) {
	queries := &fakeQueries{}
	if _, err := NewServiceWithQueries(queries).List(context.Background(), Actor{Role: "teacher"}, ListInput{}); !isCode(err, contracts.ErrorCodeRBACFORBIDDEN) {
		t.Fatalf("non-admin error = %v, want RBAC_FORBIDDEN", err)
	}
	if _, err := NewServiceWithQueries(queries).List(context.Background(), Actor{Role: "admin"}, ListInput{PageSize: 101}); !isCode(err, contracts.ErrorCodeVALIDATIONFAILED) {
		t.Fatalf("oversized page error = %v, want VALIDATION_FAILED", err)
	}

	page, err := NewServiceWithQueries(queries).List(context.Background(), Actor{Role: "admin"}, ListInput{})
	if err != nil {
		t.Fatalf("default List: %v", err)
	}
	if page.Page != defaultPage || page.PageSize != defaultPageSize {
		t.Fatalf("defaults = page %d size %d, want %d/%d", page.Page, page.PageSize, defaultPage, defaultPageSize)
	}
	if queries.listParams.RowLimit != defaultPageSize || queries.listParams.RowOffset != 0 {
		t.Fatalf("default query = %#v", queries.listParams)
	}
}

func TestList_RejectsCorruptAuditPayload(t *testing.T) {
	queries := &fakeQueries{rows: []sqlc.ListAuditLogRow{{
		ID:        pgtype.UUID{Bytes: uuid.MustParse("97000000-0000-4000-8000-000000000002"), Valid: true},
		Action:    "od.created",
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Payload:   []byte("{not-json"),
	}}}
	if _, err := NewServiceWithQueries(queries).List(context.Background(), Actor{Role: "admin"}, ListInput{}); err == nil {
		t.Fatal("List accepted an invalid audit payload")
	}
}

type fakeQueries struct {
	count       int64
	rows        []sqlc.ListAuditLogRow
	countParams sqlc.CountAuditLogParams
	listParams  sqlc.ListAuditLogParams
}

func (q *fakeQueries) CountAuditLog(_ context.Context, params sqlc.CountAuditLogParams) (int64, error) {
	q.countParams = params
	return q.count, nil
}

func (q *fakeQueries) ListAuditLog(_ context.Context, params sqlc.ListAuditLogParams) ([]sqlc.ListAuditLogRow, error) {
	q.listParams = params
	return q.rows, nil
}

func text(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: true}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return out
}

func isCode(err error, code contracts.ErrorCode) bool {
	var de domain.DomainError
	return errors.As(err, &de) && de.Code == code
}
