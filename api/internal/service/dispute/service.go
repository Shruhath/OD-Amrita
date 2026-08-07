package dispute

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

var rollPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._/-]*$`)

type Actor struct {
	UserID string
	Email  string
	Role   string
}

type Student struct {
	StudentID   string
	RollNo      string
	DisplayName string
	Branch      string
	Section     string
	Batch       string
}

type SubmitInput struct {
	StudentID              string
	SubmittedEmail         string
	EventID                string
	CategoryID             string
	ODDate                 string
	Slot                   int
	ClaimedCoordinatorName *string
	StudentNote            *string
}

type Receipt struct {
	DisputeID   string
	Reference   string
	Status      string
	SubmittedAt time.Time
	Student     Student
}

type Record struct {
	DisputeID              string
	Reference              string
	Student                Student
	SubmittedEmail         string
	EventID                string
	EventName              string
	EventDate              string
	CategoryID             string
	CategoryName           string
	ODDate                 string
	Slot                   int
	ClaimedCoordinatorName *string
	StudentNote            *string
	Status                 string
	SubmittedAt            time.Time
	ResolutionNote         *string
	ResolvedByUserID       *string
	ResolvedAt             *time.Time
}

type ListFilter struct {
	Page     int
	PageSize int
	Status   string
}

type Collection struct {
	Data     []Record
	Total    int
	Page     int
	PageSize int
}

type InsertWrite struct {
	StudentID              string
	EventID                string
	CategoryID             string
	ODDate                 string
	Slot                   int
	SubmittedEmail         string
	ClaimedCoordinatorName *string
	StudentNote            *string
	Student                Student
}

type ResolveWrite struct {
	Actor      Actor
	DisputeID  string
	Note       string
	ResolvedAt time.Time
}

type StudentReader interface {
	StudentByRollNo(ctx context.Context, rollNo string) (Student, error)
}

type Repository interface {
	Insert(ctx context.Context, tx pgx.Tx, write InsertWrite) (Record, error)
	OpenForSlot(ctx context.Context, studentID, eventID, odDate string, slot int) (Record, error)
	ListForAdmin(ctx context.Context, filter ListFilter) ([]Record, int, error)
	ListForTeacher(ctx context.Context, actorUserID string, filter ListFilter) ([]Record, int, error)
	ForResolution(ctx context.Context, actor Actor, disputeID string) (Record, error)
	Resolve(ctx context.Context, tx pgx.Tx, write ResolveWrite) (int64, error)
}

type AuditStore interface {
	WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error
}

type Service struct {
	students      StudentReader
	repo          Repository
	audited       AuditStore
	allowedDomain string
	now           func() time.Time
}

func NewService(students StudentReader, disputes Repository, audited AuditStore, allowedDomain string) *Service {
	return &Service{
		students:      students,
		repo:          disputes,
		audited:       audited,
		allowedDomain: allowedDomain,
		now:           time.Now,
	}
}

func (s *Service) Submit(ctx context.Context, input SubmitInput) (Receipt, error) {
	if err := validateSubmitInput(input, s.now()); err != nil {
		return Receipt{}, err
	}

	email := strings.ToLower(strings.TrimSpace(input.SubmittedEmail))
	if err := svcauth.EnforceDomain(email, s.allowedDomain); err != nil {
		return Receipt{}, identityUnverified()
	}
	roll, ok := rollFromEmail(email)
	if !ok {
		return Receipt{}, identityUnverified()
	}

	student, err := s.students.StudentByRollNo(ctx, roll)
	if err != nil {
		return Receipt{}, identityUnverified()
	}
	if !strings.EqualFold(student.StudentID, strings.TrimSpace(input.StudentID)) {
		return Receipt{}, identityUnverified()
	}

	write := InsertWrite{
		StudentID:              strings.TrimSpace(input.StudentID),
		EventID:                strings.TrimSpace(input.EventID),
		CategoryID:             strings.TrimSpace(input.CategoryID),
		ODDate:                 strings.TrimSpace(input.ODDate),
		Slot:                   input.Slot,
		SubmittedEmail:         email,
		ClaimedCoordinatorName: trimOptional(input.ClaimedCoordinatorName),
		StudentNote:            trimOptional(input.StudentNote),
		Student:                student,
	}

	var inserted Record
	targetTable := "disputes"
	targetID := ""
	actorRole := "student"
	payload := submitPayload(write)
	evt := store.AuditEvent{
		Action:       domain.ActionDisputeSubmitted,
		ActorAccount: &email,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		Payload:      payload,
		PayloadFunc: func() ([]byte, error) {
			return submitPayload(write), nil
		},
	}
	err = s.audited.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		record, err := s.repo.Insert(ctx, tx, write)
		if err != nil {
			return false, err
		}
		inserted = record
		targetID = record.DisputeID
		return true, nil
	})
	if err != nil {
		var de domain.DomainError
		if errors.As(err, &de) && de.Code == contracts.ErrorCodeDISPUTEALREADYOPEN {
			if open, openErr := s.repo.OpenForSlot(ctx, write.StudentID, write.EventID, write.ODDate, write.Slot); openErr == nil {
				return Receipt{}, disputeAlreadyOpen(open.Reference)
			}
			return Receipt{}, de
		}
		return Receipt{}, err
	}

	if inserted.DisputeID == "" {
		inserted.DisputeID = uuid.NewString()
	}
	if inserted.Reference == "" {
		inserted.Reference = reference(inserted.DisputeID)
	}
	if inserted.Status == "" {
		inserted.Status = "open"
	}
	if inserted.SubmittedAt.IsZero() {
		inserted.SubmittedAt = s.now()
	}
	return Receipt{
		DisputeID:   inserted.DisputeID,
		Reference:   inserted.Reference,
		Status:      inserted.Status,
		SubmittedAt: inserted.SubmittedAt,
		Student:     student,
	}, nil
}

func (s *Service) List(ctx context.Context, actor Actor, filter ListFilter) (Collection, error) {
	if actor.Role != "teacher" && actor.Role != "admin" {
		return Collection{}, forbidden()
	}
	filter = normalizeFilter(filter)
	var (
		rows  []Record
		total int
		err   error
	)
	if actor.Role == "admin" {
		rows, total, err = s.repo.ListForAdmin(ctx, filter)
	} else {
		if strings.TrimSpace(actor.UserID) == "" {
			return Collection{}, forbidden()
		}
		rows, total, err = s.repo.ListForTeacher(ctx, strings.TrimSpace(actor.UserID), filter)
	}
	if err != nil {
		return Collection{}, err
	}
	return Collection{Data: rows, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (s *Service) Resolve(ctx context.Context, actor Actor, disputeID, note string) (Record, error) {
	if actor.Role != "teacher" && actor.Role != "admin" {
		return Record{}, forbidden()
	}
	if strings.TrimSpace(disputeID) == "" {
		return Record{}, validation("dispute_id", "required", "Dispute id is required.")
	}
	if _, err := uuid.Parse(strings.TrimSpace(disputeID)); err != nil {
		return Record{}, validation("dispute_id", "invalid_format", "Dispute id must be a UUID.")
	}
	trimmedNote := strings.TrimSpace(note)
	if trimmedNote == "" {
		return Record{}, validation("resolution_note", "required", "Resolution note is required.")
	}
	if len(trimmedNote) > 2000 {
		return Record{}, validation("resolution_note", "too_long", "Resolution note must be at most 2000 characters.")
	}

	resolvedAt := s.now()
	targetTable := "disputes"
	targetID := strings.TrimSpace(disputeID)
	actorRole := actor.Role
	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	if actorAccount == "" {
		actorAccount = strings.TrimSpace(actor.UserID)
	}
	payload, _ := json.Marshal(map[string]any{
		"previous_status": "open",
		"resolution_note": trimmedNote,
		"resolved_at":     resolvedAt.UTC().Format(time.RFC3339Nano),
	})
	evt := store.AuditEvent{
		Action:       domain.ActionDisputeResolved,
		ActorAccount: &actorAccount,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		Payload:      payload,
	}

	var changed bool
	err := s.audited.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		rows, err := s.repo.Resolve(ctx, tx, ResolveWrite{
			Actor:      actor,
			DisputeID:  targetID,
			Note:       trimmedNote,
			ResolvedAt: resolvedAt,
		})
		if err != nil {
			return false, err
		}
		changed = rows > 0
		return changed, nil
	})
	if err != nil {
		return Record{}, err
	}
	if !changed {
		record, err := s.repo.ForResolution(ctx, actor, targetID)
		if err != nil {
			var de domain.DomainError
			if errors.As(err, &de) {
				return Record{}, de
			}
			return Record{}, disputeNotFound()
		}
		if record.Status == "resolved" {
			return Record{}, disputeAlreadyResolved()
		}
		return Record{}, disputeNotFound()
	}
	return s.repo.ForResolution(ctx, actor, targetID)
}

func validateSubmitInput(input SubmitInput, now time.Time) error {
	for field, value := range map[string]string{
		"student_id":      input.StudentID,
		"event_id":        input.EventID,
		"category_id":     input.CategoryID,
		"submitted_email": input.SubmittedEmail,
		"od_date":         input.ODDate,
	} {
		if strings.TrimSpace(value) == "" {
			return validation(field, "required", field+" is required.")
		}
	}
	for field, value := range map[string]string{
		"student_id":  input.StudentID,
		"event_id":    input.EventID,
		"category_id": input.CategoryID,
	} {
		if _, err := uuid.Parse(strings.TrimSpace(value)); err != nil {
			return validation(field, "invalid_format", field+" must be a UUID.")
		}
	}
	if input.Slot < 1 || input.Slot > 12 {
		return validation("slot", "out_of_range", "Slot must be between 1 and 12.")
	}
	if len(strings.TrimSpace(input.SubmittedEmail)) > 320 {
		return validation("submitted_email", "too_long", "Email must be at most 320 characters.")
	}
	if input.StudentNote != nil && len(strings.TrimSpace(*input.StudentNote)) > 1000 {
		return validation("student_note", "too_long", "Student note must be at most 1000 characters.")
	}
	if input.ClaimedCoordinatorName != nil && len(strings.TrimSpace(*input.ClaimedCoordinatorName)) > 200 {
		return validation("claimed_coordinator_name", "too_long", "Coordinator name must be at most 200 characters.")
	}
	odDate, err := time.Parse("2006-01-02", strings.TrimSpace(input.ODDate))
	if err != nil {
		return validation("od_date", "invalid_format", "OD date must be YYYY-MM-DD.")
	}
	if odDate.After(now.Add(24 * time.Hour)) {
		return validation("od_date", "out_of_range", "OD date must be within the event range.")
	}
	return nil
}

func normalizeFilter(filter ListFilter) ListFilter {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 20
	}
	if filter.PageSize > 100 {
		filter.PageSize = 100
	}
	switch filter.Status {
	case "open", "resolved", "all":
	default:
		filter.Status = "open"
	}
	return filter
}

func rollFromEmail(email string) (string, bool) {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return "", false
	}
	roll := strings.ToUpper(strings.TrimSpace(email[:at]))
	if !rollPattern.MatchString(roll) {
		return "", false
	}
	if !strings.ContainsAny(roll, "0123456789") {
		return "", false
	}
	return roll, true
}

func trimOptional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func submitPayload(write InsertWrite) []byte {
	payload, _ := json.Marshal(map[string]any{
		"student_id":               write.StudentID,
		"submitted_email":          write.SubmittedEmail,
		"event_id":                 write.EventID,
		"category_id":              write.CategoryID,
		"od_date":                  write.ODDate,
		"slot":                     write.Slot,
		"claimed_coordinator_name": write.ClaimedCoordinatorName,
		"student_note":             write.StudentNote,
	})
	return payload
}

func reference(id string) string {
	trimmed := strings.ReplaceAll(strings.TrimSpace(id), "-", "")
	if len(trimmed) >= 8 {
		return strings.ToUpper(trimmed[:8])
	}
	return strings.ToUpper(trimmed)
}

func identityUnverified() domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeDISPUTEIDENTITYUNVERIFIED,
		Message: "We could not verify that email against the roster.",
	}
}

func disputeAlreadyOpen(ref string) domain.DomainError {
	message := "A dispute is already open for this slot."
	if strings.TrimSpace(ref) != "" {
		message = "A dispute is already open for this slot. Reference: " + strings.TrimSpace(ref) + "."
	}
	return domain.DomainError{
		Code:    contracts.ErrorCodeDISPUTEALREADYOPEN,
		Message: message,
		Details: []contracts.ValidationErrorDetail{{
			Field:   "slot",
			Code:    "already_open",
			Message: message,
		}},
	}
}

func disputeNotFound() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeDISPUTENOTFOUND, Message: "Dispute not found."}
}

func disputeAlreadyResolved() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeDISPUTEALREADYRESOLVED, Message: "Dispute is already resolved."}
}

func forbidden() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Teacher or admin role required."}
}

func validation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Validation failed.",
		Details: []contracts.ValidationErrorDetail{{
			Field:   field,
			Code:    code,
			Message: message,
		}},
	}
}
