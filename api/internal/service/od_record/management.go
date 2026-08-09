package odrecord

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
	"github.com/Shruhath/OD-Amrita/api/internal/store/sqlc"
	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
)

// ManagementService owns teacher/admin correction and soft removal. Every
// mutation locks the row, writes it, and appends its immutable audit entry in
// one transaction so concurrent actions cannot audit a stale previous value.
type ManagementService struct {
	queries *sqlc.Queries
	audited AuditStore
}

func NewManagementService(pool *pgxpool.Pool, audited AuditStore) *ManagementService {
	return &ManagementService{queries: sqlc.New(pool), audited: audited}
}

type CorrectionInput struct {
	RecordID   string
	ReasonNote string
	Slot       *int
	CategoryID *string
	ODDate     *string
}

type ManagementRecord struct {
	RecordID    string
	EventID     string
	CategoryID  string
	Slot        int
	ODDate      string
	Status      string
	ReasonNote  *string
	ActorUserID *string
	ActorRole   string
}

type AuditEntry struct {
	Action     string
	ActorEmail *string
	ActorRole  *string
	OccurredAt time.Time
	Payload    map[string]any
}

func (s *ManagementService) Audit(ctx context.Context, actor Actor, recordIDText string) ([]AuditEntry, error) {
	recordID, err := managementActorAndID(actor, recordIDText)
	if err != nil {
		return nil, err
	}
	// Audit remains available after a soft removal. A removed row is precisely
	// when its history is most useful; only correction/removal reject it.
	if _, err := s.readManageableRecord(ctx, s.queries, actor, recordID); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListAuditLogForRecord(ctx, pgtype.Text{String: recordID.String(), Valid: true})
	if err != nil {
		return nil, err
	}
	entries := make([]AuditEntry, 0, len(rows))
	for _, row := range rows {
		entry := AuditEntry{Action: row.Action, OccurredAt: row.CreatedAt.Time, Payload: map[string]any{}}
		if row.ActorAccount.Valid {
			value := row.ActorAccount.String
			entry.ActorEmail = &value
		}
		if row.ActorRole.Valid {
			value := row.ActorRole.String
			entry.ActorRole = &value
		}
		if len(row.Payload) != 0 && json.Unmarshal(row.Payload, &entry.Payload) != nil {
			return nil, errors.New("odrecord: audit payload is not valid JSON")
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *ManagementService) Correct(ctx context.Context, actor Actor, input CorrectionInput) (ManagementRecord, error) {
	recordID, err := managementActorAndID(actor, input.RecordID)
	if err != nil {
		return ManagementRecord{}, err
	}
	if err := validateManagementReason(input.ReasonNote); err != nil {
		return ManagementRecord{}, err
	}
	actorID, err := uuid.Parse(strings.TrimSpace(actor.UserID))
	if err != nil {
		return ManagementRecord{}, forbidden("Teacher or administrator principal is invalid.")
	}

	var result ManagementRecord
	var payload []byte
	target, targetID := "od_records", recordID.String()
	account, role := strings.ToLower(strings.TrimSpace(actor.Email)), actor.Role
	err = s.audited.WithAudit(ctx, store.AuditEvent{
		Action: domain.ActionODOverridden, ActorAccount: &account, ActorRole: &role,
		TargetTable: &target, TargetID: &targetID,
		PayloadFunc: func() ([]byte, error) { return payload, nil },
	}, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		queries := sqlc.New(tx)
		existing, readErr := s.lockManageableRecord(ctx, queries, actor, recordID)
		if readErr != nil {
			return false, readErr
		}
		if readErr = assertActiveEventWith(ctx, queries, existing.EventID); readErr != nil {
			return false, readErr
		}
		slot, category, odDate, values, validationErr := correctionValues(ctx, queries, existing, input)
		if validationErr != nil {
			return false, validationErr
		}
		previous := recordValues(existing)
		if correctionUnchanged(existing, values) {
			return false, validation("correction", "no_change", "the correction must change slot, category_id, or od_date.")
		}
		payload, validationErr = json.Marshal(map[string]any{
			"record_id": recordID.String(), "previous": previous, "new": values,
			"reason_note": strings.TrimSpace(input.ReasonNote), "actor_user_id": actor.UserID,
			"actor_email": account, "actor_role": actor.Role,
		})
		if validationErr != nil {
			return false, validationErr
		}
		row, writeErr := queries.CorrectODRecord(ctx, sqlc.CorrectODRecordParams{
			ID: dbUUID(recordID), Slot: slot, CategoryID: category, OdDate: odDate,
			ReasonNote:  pgtype.Text{String: strings.TrimSpace(input.ReasonNote), Valid: true},
			ActorUserID: dbUUID(actorID), ActorRole: actor.Role,
		})
		if writeErr != nil {
			return false, mapWriteError(mapPgError(writeErr))
		}
		result = managementRecordFromCorrectRow(row)
		return true, nil
	})
	if err != nil {
		return ManagementRecord{}, err
	}
	return result, nil
}

func (s *ManagementService) Remove(ctx context.Context, actor Actor, recordIDText, reason string) (ManagementRecord, error) {
	recordID, err := managementActorAndID(actor, recordIDText)
	if err != nil {
		return ManagementRecord{}, err
	}
	if err := validateManagementReason(reason); err != nil {
		return ManagementRecord{}, err
	}
	actorID, err := uuid.Parse(strings.TrimSpace(actor.UserID))
	if err != nil {
		return ManagementRecord{}, forbidden("Teacher or administrator principal is invalid.")
	}

	var result ManagementRecord
	var payload []byte
	target, targetID := "od_records", recordID.String()
	account, role := strings.ToLower(strings.TrimSpace(actor.Email)), actor.Role
	err = s.audited.WithAudit(ctx, store.AuditEvent{
		Action: domain.ActionODRemoved, ActorAccount: &account, ActorRole: &role,
		TargetTable: &target, TargetID: &targetID,
		PayloadFunc: func() ([]byte, error) { return payload, nil },
	}, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		queries := sqlc.New(tx)
		existing, readErr := s.lockManageableRecord(ctx, queries, actor, recordID)
		if readErr != nil {
			return false, readErr
		}
		if readErr = assertActiveEventWith(ctx, queries, existing.EventID); readErr != nil {
			return false, readErr
		}
		payload, readErr = json.Marshal(map[string]any{
			"record_id": recordID.String(), "previous": recordValues(existing),
			"reason_note": strings.TrimSpace(reason), "actor_user_id": actor.UserID,
			"actor_email": account, "actor_role": actor.Role,
		})
		if readErr != nil {
			return false, readErr
		}
		row, writeErr := queries.RemoveODRecord(ctx, sqlc.RemoveODRecordParams{
			ID: dbUUID(recordID), ReasonNote: pgtype.Text{String: strings.TrimSpace(reason), Valid: true},
			ActorUserID: dbUUID(actorID), ActorRole: actor.Role,
		})
		if writeErr != nil {
			return false, mapWriteError(mapPgError(writeErr))
		}
		result = managementRecordFromRemoveRow(row)
		return true, nil
	})
	if err != nil {
		return ManagementRecord{}, err
	}
	return result, nil
}

func managementRecordFromCorrectRow(row sqlc.CorrectODRecordRow) ManagementRecord {
	return ManagementRecord{
		RecordID:    uuid.UUID(row.ID.Bytes).String(),
		EventID:     uuid.UUID(row.EventID.Bytes).String(),
		CategoryID:  uuid.UUID(row.CategoryID.Bytes).String(),
		Slot:        int(row.Slot),
		ODDate:      row.OdDate.Time.Format(time.DateOnly),
		Status:      row.Status,
		ReasonNote:  nullableTextPtr(row.ReasonNote),
		ActorUserID: managementActorUserID(row.ActorUserID),
		ActorRole:   row.ActorRole,
	}
}

func managementRecordFromRemoveRow(row sqlc.RemoveODRecordRow) ManagementRecord {
	return ManagementRecord{
		RecordID:    uuid.UUID(row.ID.Bytes).String(),
		EventID:     uuid.UUID(row.EventID.Bytes).String(),
		CategoryID:  uuid.UUID(row.CategoryID.Bytes).String(),
		Slot:        int(row.Slot),
		ODDate:      row.OdDate.Time.Format(time.DateOnly),
		Status:      row.Status,
		ReasonNote:  nullableTextPtr(row.ReasonNote),
		ActorUserID: managementActorUserID(row.ActorUserID),
		ActorRole:   row.ActorRole,
	}
}

func managementActorUserID(value pgtype.UUID) *string {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes).String()
	return &id
}

func nullableTextPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

func managementActorAndID(actor Actor, rawID string) (uuid.UUID, error) {
	if actor.Role != "teacher" && actor.Role != "admin" {
		return uuid.Nil, forbidden("Teacher or administrator access is required.")
	}
	recordID, err := uuid.Parse(strings.TrimSpace(rawID))
	if err != nil {
		return uuid.Nil, validation("record_id", "invalid_format", "record_id must be a UUID.")
	}
	return recordID, nil
}

func (s *ManagementService) readManageableRecord(ctx context.Context, queries *sqlc.Queries, actor Actor, recordID uuid.UUID) (sqlc.GetODRecordByIDRow, error) {
	row, err := queries.GetODRecordByID(ctx, dbUUID(recordID))
	if errors.Is(err, pgx.ErrNoRows) {
		return row, domain.DomainError{Code: contracts.ErrorCodeODRECORDNOTFOUND, Message: "OD record was not found."}
	}
	if err != nil {
		return row, err
	}
	if authErr := assertTeacherManagesRecord(ctx, queries, actor, recordID); authErr != nil {
		return row, authErr
	}
	return row, nil
}

// lockManageableRecord resolves and row-locks the target record for a write.
// RBAC runs before the terminal-status check so a caller with no authority
// over the record always sees a uniform RBAC_FORBIDDEN — never learning
// whether an out-of-scope record exists or has already been removed.
func (s *ManagementService) lockManageableRecord(ctx context.Context, queries *sqlc.Queries, actor Actor, recordID uuid.UUID) (sqlc.LockODRecordByIDRow, error) {
	row, err := queries.LockODRecordByID(ctx, dbUUID(recordID))
	if errors.Is(err, pgx.ErrNoRows) {
		return row, domain.DomainError{Code: contracts.ErrorCodeODRECORDNOTFOUND, Message: "OD record was not found."}
	}
	if err != nil {
		return row, err
	}
	if authErr := assertTeacherManagesRecord(ctx, queries, actor, recordID); authErr != nil {
		return row, authErr
	}
	if row.Status != "present" {
		return row, domain.DomainError{Code: contracts.ErrorCodeODRECORDALREADYREMOVED, Message: "OD record has already been removed."}
	}
	return row, nil
}

// assertTeacherManagesRecord is a no-op for admin (unconditional access, same
// asymmetry as the rest of this codebase's RBAC checks). For a teacher it
// confirms the record's student sits in a branch they teach.
func assertTeacherManagesRecord(ctx context.Context, queries *sqlc.Queries, actor Actor, recordID uuid.UUID) error {
	if actor.Role != "teacher" {
		return nil
	}
	userID, parseErr := uuid.Parse(strings.TrimSpace(actor.UserID))
	if parseErr != nil {
		return forbidden("Teacher principal is invalid.")
	}
	allowed, queryErr := queries.TeacherCanManageODRecord(ctx, sqlc.TeacherCanManageODRecordParams{UserID: dbUUID(userID), RecordID: dbUUID(recordID)})
	if queryErr != nil {
		return queryErr
	}
	if !allowed {
		return forbidden("Teacher may manage only records in taught branches.")
	}
	return nil
}

func assertActiveEventWith(ctx context.Context, queries *sqlc.Queries, eventID pgtype.UUID) error {
	event, err := queries.GetMarkEvent(ctx, eventID)
	if err != nil {
		return err
	}
	if event.Status == "archived" {
		return eventArchived()
	}
	return nil
}

func correctionValues(ctx context.Context, queries *sqlc.Queries, existing sqlc.LockODRecordByIDRow, input CorrectionInput) (pgtype.Int4, pgtype.UUID, pgtype.Date, map[string]any, error) {
	values := map[string]any{"slot": existing.Slot, "category_id": uuid.UUID(existing.CategoryID.Bytes).String(), "od_date": existing.OdDate.Time.Format(time.DateOnly), "reason_note": strings.TrimSpace(input.ReasonNote)}
	var slot pgtype.Int4
	if input.Slot != nil {
		if *input.Slot < 1 || *input.Slot > 12 {
			return slot, pgtype.UUID{}, pgtype.Date{}, nil, validation("slot", "range", "slot must be between 1 and 12.")
		}
		slot = pgtype.Int4{Int32: int32(*input.Slot), Valid: true}
		values["slot"] = slot.Int32
	}
	var category pgtype.UUID
	if input.CategoryID != nil {
		parsed, err := uuid.Parse(strings.TrimSpace(*input.CategoryID))
		if err != nil {
			return slot, category, pgtype.Date{}, nil, validation("category_id", "invalid_format", "category_id must be a UUID.")
		}
		if _, err := queries.GetMarkCategoryForEvent(ctx, sqlc.GetMarkCategoryForEventParams{EventID: existing.EventID, CategoryID: dbUUID(parsed)}); errors.Is(err, pgx.ErrNoRows) {
			return slot, category, pgtype.Date{}, nil, categoryNotFound()
		} else if err != nil {
			return slot, category, pgtype.Date{}, nil, err
		}
		category = dbUUID(parsed)
		values["category_id"] = parsed.String()
	}
	var odDate pgtype.Date
	if input.ODDate != nil {
		trimmed := strings.TrimSpace(*input.ODDate)
		day, err := time.Parse(time.DateOnly, trimmed)
		if err != nil || day.Format(time.DateOnly) != trimmed {
			return slot, category, odDate, nil, validation("od_date", "invalid_format", "od_date must be a valid YYYY-MM-DD date.")
		}
		odDate = pgtype.Date{Time: day, Valid: true}
		values["od_date"] = odDate.Time.Format(time.DateOnly)
	}
	if !slot.Valid && !category.Valid && !odDate.Valid {
		return slot, category, odDate, nil, validation("correction", "no_change", "at least one correctable field is required.")
	}
	return slot, category, odDate, values, nil
}

func correctionUnchanged(existing sqlc.LockODRecordByIDRow, values map[string]any) bool {
	return values["slot"] == existing.Slot && values["category_id"] == uuid.UUID(existing.CategoryID.Bytes).String() && values["od_date"] == existing.OdDate.Time.Format(time.DateOnly)
}

func recordValues(row sqlc.LockODRecordByIDRow) map[string]any {
	return map[string]any{"slot": row.Slot, "category_id": uuid.UUID(row.CategoryID.Bytes).String(), "od_date": row.OdDate.Time.Format(time.DateOnly), "reason_note": nullableString(row.ReasonNote)}
}

func validateManagementReason(reason string) error {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return validation("reason_note", "required", "reason_note is required.")
	}
	if len([]rune(trimmed)) > 2000 {
		return validation("reason_note", "max_length", "reason_note must not exceed 2000 characters.")
	}
	return nil
}

func nullableString(value pgtype.Text) any {
	if !value.Valid {
		return nil
	}
	return value.String
}
