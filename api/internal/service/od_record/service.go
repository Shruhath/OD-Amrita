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
	ReasonNote    *string
}

type Event struct {
	EventID string
	// EventDate is the FIRST day of the range and EndDate the last; they are
	// equal for a one-day event. Both are needed to decide whether a slot is open
	// and to clamp a correction back inside the event.
	EventDate time.Time
	EndDate   time.Time
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
	Student    Student
	StudentID  string
	EventID    string
	CategoryID string
	Slot       int
	// ODDate is the day the OD BELONGS to, derived server-side in campus time and
	// never accepted from a client. ServerReceivedAt is when the row arrived, and
	// on the correction path the two are different days.
	ODDate           time.Time
	CaptureMethod    CaptureMethod
	ActorUserID      string
	ActorRole        string
	ReasonNote       *string
	ServerReceivedAt time.Time
}

type MarkResult struct {
	RecordID         string
	Student          Student
	EventID          string
	CategoryID       string
	Slot             int
	ODDate           time.Time
	CaptureMethod    CaptureMethod
	ReasonNote       *string
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

// ScopeGuard is the marking scope seam, seen from the mark service.
//
// It is a dependency rather than a direct call so this package keeps no opinion
// about who may mark: the whole policy lives in service/marking, and Story 0.7
// replaces its body without touching a line of the mark path.
type ScopeGuard interface {
	AssertCanMark(ctx context.Context, actor Actor, eventID, categoryID string) error
}

type StudentScopeGuard interface {
	AssertCanMarkStudent(ctx context.Context, actor Actor, eventID, categoryID string, student Student) error
}

// AllowAnyScope admits every scope. It is the EXPLICIT opt-out for tests that
// are not about scope at all, and it is exported precisely so choosing it has to
// be written down at the call site.
//
// It is deliberately NOT a fallback for a nil guard. It used to be: NewService
// mapped a nil ScopeGuard onto it, under a comment claiming that made a nil
// guard "fail loudly". It did the exact opposite — a composition root that
// forgot the argument got a mark endpoint with no scope enforcement at all,
// silently, which is the precise NFR11 property the seam exists to hold. A
// missing guard now panics; a deliberately absent one says so in source.
type AllowAnyScope struct{}

func (AllowAnyScope) AssertCanMark(context.Context, Actor, string, string) error { return nil }
func (AllowAnyScope) AssertCanMarkStudent(context.Context, Actor, string, string, Student) error {
	return nil
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// SlotCeiling reports whether the evening slots (13..20) are currently
// admitted.
//
// An INTERFACE declared here rather than an import of the settings service, so
// this package keeps depending on nothing but its own contracts — and so a test
// can state the ceiling directly instead of standing up a database.
//
// A nil ceiling means the standard teaching day. That is what every existing
// construction of this service gets, so the switch cannot change behaviour
// anywhere that has not opted in.
type SlotCeiling interface {
	ExtendedSlotsEnabled(ctx context.Context) (bool, error)
}

type Service struct {
	reader       Reader
	cardResolver CardResolver
	writer       Writer
	audited      AuditStore
	clock        Clock
	scope        ScopeGuard
	credits      CreditLedger
	slotCeiling  SlotCeiling
}

// WithSlotCeiling opts this service into the extended-slots switch.
//
// A setter rather than a constructor parameter because the ceiling is optional
// and every caller that does not pass one must keep the behaviour it has today.
// Adding it to NewService would have made "did somebody remember to pass this?"
// a question, and the wrong answer widens what the marking path accepts.
func (s *Service) WithSlotCeiling(ceiling SlotCeiling) *Service {
	s.slotCeiling = ceiling
	return s
}

// maxSlot is the ceiling in force for this request.
//
// A read failure is returned, NOT defaulted: the caller is the marking path, and
// quietly falling back to 12 during a database blip would refuse a coordinator
// standing in slot 14 with a validation error naming a bound that is not the one
// in force.
func (s *Service) maxSlot(ctx context.Context) (int, error) {
	if s.slotCeiling == nil {
		return StandardSlotCount, nil
	}
	extended, err := s.slotCeiling.ExtendedSlotsEnabled(ctx)
	if err != nil {
		return 0, err
	}
	return SlotCount(extended), nil
}

func NewService(reader Reader, cardResolver CardResolver, writer Writer, audited AuditStore, clock Clock, scope ScopeGuard, credits CreditLedger) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	// Fail at composition time, not at the first mark of the event. A nil guard
	// is never a legitimate production state, and silently substituting an
	// allow-everything policy would defeat the seam without any signal.
	if scope == nil {
		panic("od_record: NewService requires a ScopeGuard — pass AllowAnyScope{} to opt out explicitly in a test that is not about scope")
	}
	if credits == nil {
		panic("od_record: NewService requires a CreditLedger")
	}
	return &Service{reader: reader, cardResolver: cardResolver, writer: writer, audited: audited, clock: clock, scope: scope, credits: credits}
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
	// Scoped to capture_method == search: nfc/camera marks stay note-free
	// regardless of role (AC 2) — a teacher/admin nfc/camera request is rejected
	// downstream by slotFor's capture-method check, not by a note requirement
	// that was never the actual reason for the rejection.
	if actor.Role != "coordinator" && input.CaptureMethod == CaptureMethodSearch {
		if input.ReasonNote == nil || strings.TrimSpace(*input.ReasonNote) == "" {
			return MarkResult{}, validation("reason_note", "required", "reason_note is required for teacher and admin marks.")
		}
		trimmed := strings.TrimSpace(*input.ReasonNote)
		if len([]rune(trimmed)) > 2000 {
			return MarkResult{}, validation("reason_note", "max_length", "reason_note must not exceed 2000 characters.")
		}
		input.ReasonNote = &trimmed
	}
	if err := validateIdentityCapturePair(input); err != nil {
		return MarkResult{}, err
	}
	if _, err := uuid.Parse(strings.TrimSpace(input.EventID)); err != nil {
		return MarkResult{}, validation("event_id", "invalid_format", "event_id must be a UUID.")
	}
	if _, err := uuid.Parse(strings.TrimSpace(input.CategoryID)); err != nil {
		return MarkResult{}, validation("category_id", "invalid_format", "category_id must be a UUID.")
	}

	if actor.Role == "coordinator" {
		// The coordinator scope gate stays in front of every read and write on
		// this path. Enforcing scope in the read handlers alone would be worthless:
		// the client chooses these ids and could post any pair it liked (NFR11).
		if err := s.scope.AssertCanMark(ctx, actor, input.EventID, input.CategoryID); err != nil {
			return MarkResult{}, err
		}
	}

	// The actor's id is only needed to write the record and its audit row, so it
	// is checked here rather than with the request shape: a caller refused by the
	// scope gate above must not be able to learn anything from what comes after it.
	if err := validateActorID(actor); err != nil {
		return MarkResult{}, err
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

	if actor.Role != "coordinator" {
		studentScope, ok := s.scope.(StudentScopeGuard)
		if !ok {
			return MarkResult{}, forbidden("Teacher/admin mark authorization is not configured.")
		}
		if err := studentScope.AssertCanMarkStudent(ctx, actor, input.EventID, input.CategoryID, student); err != nil {
			return MarkResult{}, err
		}
	}

	slot, odDate, serverReceivedAt, err := s.slotFor(ctx, actor, input, event)
	if err != nil {
		var de domain.DomainError
		if errors.As(err, &de) && de.Code == contracts.ErrorCodeSLOTCLOSED {
			if auditErr := s.writeBlockedAudit(ctx, actor, input, serverReceivedAt); auditErr != nil {
				return MarkResult{}, auditErr
			}
		}
		return MarkResult{}, err
	}

	record := RecordInput{
		Student:          student,
		StudentID:        student.StudentID,
		EventID:          input.EventID,
		CategoryID:       input.CategoryID,
		Slot:             slot,
		ODDate:           odDate,
		CaptureMethod:    input.CaptureMethod,
		ActorUserID:      actor.UserID,
		ActorRole:        actor.Role,
		ReasonNote:       input.ReasonNote,
		ServerReceivedAt: serverReceivedAt,
	}
	result, err := s.writePresent(ctx, actor, record)
	if err != nil {
		var exhausted CaptureCreditExhaustedError
		if errors.As(err, &exhausted) {
			if auditErr := s.writeCaptureCreditBlockedAudit(ctx, actor, input, serverReceivedAt, exhausted.State); auditErr != nil {
				return MarkResult{}, auditErr
			}
			return MarkResult{}, captureCreditLimitReached()
		}
		return MarkResult{}, mapWriteError(err)
	}
	return result, nil
}

// validateActor is the ROLE gate, and it runs before anything else so an
// unauthorized caller cannot tell a well-formed request from a malformed one.
func validateActor(actor Actor) error {
	switch actor.Role {
	case "coordinator", "teacher", "admin":
		return nil
	case "student":
		return forbidden("Students cannot mark attendance.")
	default:
		return forbidden("Coordinator, teacher, or admin role required.")
	}
}

// validateActorID is the separate format check, run after authorization.
func validateActorID(actor Actor) error {
	if _, err := uuid.Parse(strings.TrimSpace(actor.UserID)); err != nil {
		return validation("actor_user_id", "invalid_format", "authenticated user id must be a UUID.")
	}
	return nil
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

// validateIdentityCapturePair pins which identity may arrive with which capture
// method.
//
// `card` is valid for nfc and camera. `student_id` is valid for camera and
// search — camera pairs with BOTH because nothing in this MVP ever writes a
// sealed token to a barcode or QR: the card populator writes the token to the
// NFC tag, and what the camera actually reads is the barcode already printed on
// the university ID card, which carries a ROLL NUMBER. The client resolves that
// roll to a student_id through the search endpoint and marks with
// capture_method "camera". Without this the three-mode screen has no legal
// request shape for a camera mark at all.
//
// It opens no new fraud surface: camera and search share one credit pool in
// Story 0.8, and capture_method is still persisted truthfully on the od_records
// row and on its audit row (FR44), so a camera mark can never be counted or
// audited as something else.
func validateIdentityCapturePair(input MarkInput) error {
	if input.Card != nil {
		switch input.CaptureMethod {
		case CaptureMethodNFC, CaptureMethodCamera:
			return nil
		default:
			return validation("identity", "capture_method_mismatch", "card identity is valid only for nfc or camera marks.")
		}
	}
	if input.StudentID != nil {
		switch input.CaptureMethod {
		case CaptureMethodCamera, CaptureMethodSearch:
			return nil
		default:
			return validation("identity", "capture_method_mismatch", "student_id identity is valid only for camera or search marks.")
		}
	}
	return nil
}

// slotFor resolves the slot AND the day the OD belongs to from ONE clock read.
//
// The day is returned alongside the slot rather than derived later because the
// counters, the credit debit and the record write must all agree about which day
// it is; two clock reads either side of midnight would not.
func (s *Service) slotFor(ctx context.Context, actor Actor, input MarkInput, event Event) (int, time.Time, time.Time, error) {
	now := s.clock.Now()
	today := CampusDate(now)
	ceiling, ceilingErr := s.maxSlot(ctx)
	if ceilingErr != nil {
		return 0, today, now, ceilingErr
	}
	switch actor.Role {
	case "coordinator":
		if input.Slot != nil {
			return 0, today, now, validation("slot", "forbidden", "coordinator marks use server-derived slot only.")
		}
		slot, _, ok := ActiveSlot(now, event.EventDate, event.EndDate, ceiling)
		if !ok {
			return 0, today, now, slotClosed()
		}
		// ActiveSlot already proved today is inside the range, so the campus date
		// needs no clamp here — and must not get one: a live mark files under the
		// day it happened or not at all.
		return slot, today, now, nil
	case "teacher", "admin":
		if input.CaptureMethod != CaptureMethodSearch {
			return 0, today, now, forbidden("Teacher and admin MVP marks are search-only.")
		}
		if input.Slot == nil {
			return 0, today, now, validation("slot", "required", "slot is required for teacher and admin search marks.")
		}
		if *input.Slot < 1 || *input.Slot > ceiling {
			return 0, today, now, validation(
				"slot", "range", fmt.Sprintf("slot must be between 1 and %d.", ceiling))
		}
		// The CORRECTION path: exempt from slot windows, so it legitimately runs
		// the morning after — or days after a one-day event. Campus today would
		// then be outside the range and the database's range guard would refuse a
		// correction the teacher is entitled to make, so it is clamped to the
		// nearest bound.
		//
		// Story 0.12b replaces this default with a day the teacher picks, which is
		// the only way to file a Tuesday correction under Monday.
		return *input.Slot, ClampToEventRange(today, event.EventDate, event.EndDate), now, nil
	default:
		return 0, today, now, forbidden("Coordinator, teacher, or admin role required.")
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
			"od_date":            record.ODDate.Format("2006-01-02"),
			"capture_method":     string(record.CaptureMethod),
			"reason_note":        record.ReasonNote,
			"actor_user_id":      actor.UserID,
			"actor_email":        actorAccount,
			"actor_role":         actor.Role,
			"server_received_at": record.ServerReceivedAt.Format(time.RFC3339Nano),
			"context":            "online_live",
		})
	}
	err := s.audited.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		if consumesFallbackCredit(actor, record.CaptureMethod) {
			if _, err := s.credits.Debit(ctx, tx, record.EventID, actor.UserID, record.ODDate); err != nil {
				return false, err
			}
		}
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

func (s *Service) writeCaptureCreditBlockedAudit(ctx context.Context, actor Actor, input MarkInput, serverReceivedAt time.Time, state FallbackCreditState) error {
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
			"limit":              state.Limit,
			"consumed":           state.Consumed,
			"remaining":          0,
			"reason":             "capture_credit_exhausted",
			"context":            "online_live",
		})
	}
	return s.audited.WithAudit(ctx, evt, func(context.Context, pgx.Tx) (bool, error) {
		return true, nil
	})
}

func consumesFallbackCredit(actor Actor, method CaptureMethod) bool {
	return actor.Role == "coordinator" && (method == CaptureMethodCamera || method == CaptureMethodSearch)
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
