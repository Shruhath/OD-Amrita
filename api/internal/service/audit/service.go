package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
)

const (
	defaultPage     = 1
	defaultPageSize = 50
	maxPage         = 100000
	maxPageSize     = 100
)

type Actor struct {
	UserID string
	Email  string
	Role   string
}

type ListInput struct {
	Action       string
	ActorAccount string
	TargetTable  string
	TargetID     string
	Page         int
	PageSize     int
}

type Entry struct {
	ID           string
	Action       string
	ActorAccount *string
	ActorRole    *string
	TargetTable  *string
	TargetID     *string
	OccurredAt   time.Time
	Payload      map[string]any
}

type Page struct {
	Data     []Entry
	Page     int
	PageSize int
	Total    int
}

type queryer interface {
	CountAuditLog(context.Context, sqlc.CountAuditLogParams) (int64, error)
	ListAuditLog(context.Context, sqlc.ListAuditLogParams) ([]sqlc.ListAuditLogRow, error)
}

type Service struct {
	queries queryer
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{queries: sqlc.New(pool)}
}

func NewServiceWithQueries(queries queryer) *Service {
	return &Service{queries: queries}
}

func (s *Service) List(ctx context.Context, actor Actor, input ListInput) (Page, error) {
	if actor.Role != "admin" {
		return Page{}, domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Administrator access is required."}
	}
	normalized, err := normalize(input)
	if err != nil {
		return Page{}, err
	}
	params := sqlc.CountAuditLogParams{
		Action:       normalized.Action,
		ActorAccount: normalized.ActorAccount,
		TargetTable:  normalized.TargetTable,
		TargetID:     normalized.TargetID,
	}
	total, err := s.queries.CountAuditLog(ctx, params)
	if err != nil {
		return Page{}, err
	}
	rows, err := s.queries.ListAuditLog(ctx, sqlc.ListAuditLogParams{
		Action:       params.Action,
		ActorAccount: params.ActorAccount,
		TargetTable:  params.TargetTable,
		TargetID:     params.TargetID,
		RowOffset:    int64((normalized.Page - 1) * normalized.PageSize),
		RowLimit:     int64(normalized.PageSize),
	})
	if err != nil {
		return Page{}, err
	}
	data := make([]Entry, 0, len(rows))
	for _, row := range rows {
		entry, mapErr := entryFromRow(row)
		if mapErr != nil {
			return Page{}, mapErr
		}
		data = append(data, entry)
	}
	return Page{Data: data, Page: normalized.Page, PageSize: normalized.PageSize, Total: int(total)}, nil
}

func normalize(input ListInput) (ListInput, error) {
	input.Action = strings.TrimSpace(input.Action)
	input.ActorAccount = strings.ToLower(strings.TrimSpace(input.ActorAccount))
	input.TargetTable = strings.TrimSpace(input.TargetTable)
	input.TargetID = strings.TrimSpace(input.TargetID)
	if input.Page == 0 {
		input.Page = defaultPage
	}
	if input.PageSize == 0 {
		input.PageSize = defaultPageSize
	}
	if input.Page < 1 || input.Page > maxPage {
		return ListInput{}, validation("page", "range", "page is outside the permitted range.")
	}
	if input.PageSize < 1 || input.PageSize > maxPageSize {
		return ListInput{}, validation("page_size", "range", "page_size is outside the permitted range.")
	}
	if err := boundedText("action", input.Action, 120); err != nil {
		return ListInput{}, err
	}
	if err := boundedText("actor_account", input.ActorAccount, 320); err != nil {
		return ListInput{}, err
	}
	if err := boundedText("target_table", input.TargetTable, 120); err != nil {
		return ListInput{}, err
	}
	if err := boundedText("target_id", input.TargetID, 200); err != nil {
		return ListInput{}, err
	}
	return input, nil
}

func boundedText(field, value string, maxRunes int) error {
	if value == "" {
		return nil
	}
	if !utf8.ValidString(value) {
		return validation(field, "invalid_encoding", field+" must be valid UTF-8 text.")
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return validation(field, "maximum", field+" is too long.")
	}
	return nil
}

func entryFromRow(row sqlc.ListAuditLogRow) (Entry, error) {
	id := uuid.UUID(row.ID.Bytes).String()
	entry := Entry{
		ID:           id,
		Action:       row.Action,
		ActorAccount: textPtr(row.ActorAccount),
		ActorRole:    textPtr(row.ActorRole),
		TargetTable:  textPtr(row.TargetTable),
		TargetID:     textPtr(row.TargetID),
		OccurredAt:   row.CreatedAt.Time,
	}
	if len(row.Payload) != 0 {
		payload := map[string]any{}
		if err := json.Unmarshal(row.Payload, &payload); err != nil {
			return Entry{}, errors.New("audit: payload is not valid JSON")
		}
		entry.Payload = payload
	}
	return entry, nil
}

func textPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func validation(field, code, message string) error {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Audit log query validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}
