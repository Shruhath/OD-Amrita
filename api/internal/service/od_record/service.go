package odrecord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

var (
	ErrEventNotFound          = errors.New("odrecord: event not found")
	ErrCategoryNotFound       = errors.New("odrecord: category not found")
	ErrStudentNotFound        = errors.New("odrecord: student not found")
	ErrCardNotRecognized      = errors.New("odrecord: card not recognized")
	ErrDuplicatePresentRecord = errors.New("odrecord: duplicate present record")
	ErrEventArchived          = errors.New("odrecord: event archived")
)

type CaptureMethod string

const (
	CaptureMethodNFC    CaptureMethod = "nfc"
	CaptureMethodCamera CaptureMethod = "camera"
	CaptureMethodSearch CaptureMethod = "search"
)

type Actor struct {
	UserID string
	Email  string
	Role   string
}

type CardInput struct {
	Version string
	Token   string
}

type MarkInput struct {
	EventID       string
	CategoryID    string
	CaptureMethod CaptureMethod
	StudentID     *string
	Card          *CardInput
	Slot          *int
}

type Event struct {
	EventID   string
	EventDate time.Time
	Status    string
}

type Category struct {
	CategoryID string
	EventID    string
}

type Student struct {
	StudentID   string
	RollNo      string
	DisplayName string
	Branch      string
	Section     string
	Batch       string
}

type RecordInput struct {
	Student          Student
	StudentID        string
	EventID          string
	CategoryID       string
	Slot             int
	CaptureMethod    CaptureMethod
	ActorUserID      string
	ActorRole        string
	ServerReceivedAt time.Time
}

type MarkResult struct {
	RecordID         string
	Student          Student
	EventID          string
	CategoryID       string
	Slot             int
	CaptureMethod    CaptureMethod
	ServerReceivedAt time.Time
}

type Reader interface {
	EventForMark(context.Context, string) (Event, error)
	CategoryForEvent(context.Context, string, string) (Category, error)
	StudentForMark(context.Context, string) (Student, error)
}

type CardResolver interface {
	Resolve(context.Context, Actor, CardInput) (Student, error)
}

type Writer interface {
	InsertPresent(context.Context, pgx.Tx, RecordInput) (MarkResult, error)
}

type AuditStore interface {
	WithAudit(context.Context, store.AuditEvent, func(context.Context, pgx.Tx) (bool, error)) error
}

type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type Service struct {
	reader       Reader
	cardResolver CardResolver
	writer       Writer
	audited      AuditStore
	clock        Clock
}

func NewService(reader Reader, cardResolver CardResolver, writer Writer, audited AuditStore, clock Clock) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	return &Service{reader: reader, cardResolver: cardResolver, writer: writer, audited: audited, clock: clock}
}

func (s *Service) Mark(ctx context.Context, actor Actor, input MarkInput) (MarkResult, error) {
	if err := validateActor(actor); err != nil {
		return MarkResult{}, err
	}
	if err := validateIdentity(input); err != nil {
		return MarkResult{}, err
	}
	if !validCaptureMethod(input.CaptureMethod) {
		return MarkResult{}, validation("capture_method", "unsupported", "capture_method must be one of nfc, camera, or search.")
	}
	if _, err := uuid.Parse(strings.TrimSpace(input.EventID)); err != nil {
		return MarkResult{}, validation("event_id", "invalid_format", "event_id must be a UUID.")
	}
	if _, err := uuid.Parse(strings.TrimSpace(input.CategoryID)); err != nil {
		return MarkResult{}, validation("category_id", "invalid_format", "category_id must be a UUID.")
	}

	event, err := s.reader.EventForMark(ctx, input.EventID)
	if errors.Is(err, ErrEventNotFound) {
		return MarkResult{}, eventNotFound()
	}
	if err != nil {
		return MarkResult{}, err
	}
	if event.Status == "archived" {
		return MarkResult{}, eventArchived()
	}
	if _, err := s.reader.CategoryForEvent(ctx, input.EventID, input.CategoryID); errors.Is(err, ErrCategoryNotFound) {
		return MarkResult{}, categoryNotFound()
	} else if err != nil {
		return MarkResult{}, err
	}

	slot, serverReceivedAt, err := s.slotFor(actor, input, event)
	if err != nil {
		var de domain.DomainError
		if errors.As(err, &de) && de.Code == contracts.ErrorCodeSLOTCLOSED {
			if auditErr := s.writeBlockedAudit(ctx, actor, input, serverReceivedAt); auditErr != nil {
				return MarkResult{}, auditErr
			}
		}
		return MarkResult{}, err
	}

	student, err := s.resolveStudent(ctx, actor, input)
	if errors.Is(err, ErrStudentNotFound) {
		return MarkResult{}, studentNotFound()
	}
	if errors.Is(err, ErrCardNotRecognized) {
		return MarkResult{}, cardNotRecognized()
	}
	if err != nil {
		return MarkResult{}, err
	}

	record := RecordInput{
		Student:          student,
		StudentID:        student.StudentID,
		EventID:          input.EventID,
		CategoryID:       input.CategoryID,
		Slot:             slot,
		CaptureMethod:    input.CaptureMethod,
		ActorUserID:      actor.UserID,
		ActorRole:        actor.Role,
		ServerReceivedAt: serverReceivedAt,
	}
	result, err := s.writePresent(ctx, actor, record)
	if err != nil {
		return MarkResult{}, mapWriteError(err)
	}
	return result, nil
}

func validateActor(actor Actor) error {
	switch actor.Role {
	case "coordinator", "teacher", "admin":
		if _, err := uuid.Parse(strings.TrimSpace(actor.UserID)); err != nil {
			return validation("actor_user_id", "invalid_format", "authenticated user id must be a UUID.")
		}
		return nil
	case "student":
		return forbidden("Students cannot mark attendance.")
	default:
		return forbidden("Coordinator, teacher, or admin role required.")
	}
}

func validateIdentity(input MarkInput) error {
	hasStudent := input.StudentID != nil && strings.TrimSpace(*input.StudentID) != ""
	hasCard := input.Card != nil
	if hasStudent == hasCard {
		return validation("identity", "exactly_one", "Provide exactly one of student_id or card.")
	}
	if hasStudent {
		if _, err := uuid.Parse(strings.TrimSpace(*input.StudentID)); err != nil {
			return validation("student_id", "invalid_format", "student_id must be a UUID.")
		}
	}
	if hasCard && (strings.TrimSpace(input.Card.Version) == "" || strings.TrimSpace(input.Card.Token) == "") {
		return cardNotRecognized()
	}
	return nil
}

func (s *Service) slotFor(actor Actor, input MarkInput, event Event) (int, time.Time, error) {
	now := s.clock.Now()
	switch actor.Role {
	case "coordinator":
		if input.Slot != nil {
			return 0, now, validation("slot", "forbidden", "coordinator marks use server-derived slot only.")
		}
		slot, _, ok := ActiveSlot(now, event.EventDate)
		if !ok {
			return 0, now, slotClosed()
		}
		return slot, now, nil
	case "teacher", "admin":
		if input.CaptureMethod != CaptureMethodSearch {
			return 0, now, forbidden("Teacher and admin MVP marks are search-only.")
		}
		if input.Slot == nil {
			return 0, now, validation("slot", "required", "slot is required for teacher and admin search marks.")
		}
		if *input.Slot < 1 || *input.Slot > 12 {
			return 0, now, validation("slot", "range", "slot must be between 1 and 12.")
		}
		return *input.Slot, now, nil
	default:
		return 0, now, forbidden("Coordinator, teacher, or admin role required.")
	}
}

func (s *Service) resolveStudent(ctx context.Context, actor Actor, input MarkInput) (Student, error) {
	if input.Card != nil {
		return s.cardResolver.Resolve(ctx, actor, *input.Card)
	}
	return s.reader.StudentForMark(ctx, strings.TrimSpace(*input.StudentID))
}

func (s *Service) writePresent(ctx context.Context, actor Actor, record RecordInput) (MarkResult, error) {
	targetTable := "od_records"
	targetID := ""
	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	var result MarkResult
	evt := store.AuditEvent{
		Action:       domain.ActionODCreated,
		ActorAccount: stringPtr(actorAccount),
		ActorRole:    stringPtr(actorRole),
		TargetTable:  &targetTable,
		TargetID:     &targetID,
	}
	evt.PayloadFunc = func() ([]byte, error) {
		return json.Marshal(map[string]any{
			"record_id":          targetID,
			"student_id":         record.StudentID,
			"event_id":           record.EventID,
			"category_id":        record.CategoryID,
			"slot":               record.Slot,
			"capture_method":     string(record.CaptureMethod),
			"actor_user_id":      actor.UserID,
			"actor_email":        actorAccount,
			"actor_role":         actor.Role,
			"server_received_at": record.ServerReceivedAt.Format(time.RFC3339Nano),
			"context":            "online_live",
		})
	}
	err := s.audited.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		written, err := s.writer.InsertPresent(ctx, tx, record)
		if err != nil {
			return false, err
		}
		result = written
		targetID = written.RecordID
		return true, nil
	})
	return result, err
}

func (s *Service) writeBlockedAudit(ctx context.Context, actor Actor, input MarkInput, serverReceivedAt time.Time) error {
	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "od_records"
	evt := store.AuditEvent{
		Action:       domain.ActionScanBlocked,
		ActorAccount: stringPtr(actorAccount),
		ActorRole:    stringPtr(actorRole),
		TargetTable:  &targetTable,
	}
	evt.PayloadFunc = func() ([]byte, error) {
		return json.Marshal(map[string]any{
			"actor_user_id":      actor.UserID,
			"actor_email":        actorAccount,
			"actor_role":         actor.Role,
			"event_id":           input.EventID,
			"category_id":        input.CategoryID,
			"capture_method":     string(input.CaptureMethod),
			"server_received_at": serverReceivedAt.Format(time.RFC3339Nano),
			"reason":             "slot_closed",
			"context":            "online_live",
		})
	}
	return s.audited.WithAudit(ctx, evt, func(context.Context, pgx.Tx) (bool, error) {
		return true, nil
	})
}

func validCaptureMethod(method CaptureMethod) bool {
	switch method {
	case CaptureMethodNFC, CaptureMethodCamera, CaptureMethodSearch:
		return true
	default:
		return false
	}
}

func mapWriteError(err error) error {
	switch {
	case errors.Is(err, ErrDuplicatePresentRecord):
		return duplicate()
	case errors.Is(err, ErrEventArchived):
		return eventArchived()
	case errors.Is(err, ErrCategoryNotFound):
		return categoryNotFound()
	case errors.Is(err, ErrStudentNotFound):
		return studentNotFound()
	default:
		return err
	}
}

func validation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Mark request validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func forbidden(message string) domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: message}
}

func eventNotFound() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeEVENTNOTFOUND, Message: "Event not found."}
}

func eventArchived() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeEVENTARCHIVED, Message: "Event is archived."}
}

func categoryNotFound() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeCATEGORYNOTFOUND, Message: "Category not found."}
}

func studentNotFound() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeSTUDENTNOTFOUND, Message: "Student not found."}
}

func cardNotRecognized() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeCARDNOTRECOGNIZED, Message: "Card not recognized."}
}

func duplicate() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeSCANDUPLICATE, Message: "Student is already marked present for this slot."}
}

func slotClosed() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeSLOTCLOSED, Message: "No active attendance slot is open."}
}

func stringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func invalidRowError(entity string) error {
	return fmt.Errorf("odrecord: %s row contains invalid required values", entity)
}
