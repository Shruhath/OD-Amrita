package dispute

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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
	// Outcome is nil while the dispute is open, and "approved" or "rejected"
	// once it is resolved. It is what decides whether the slot gets credited, so
	// it is a stored fact rather than something inferred from the note.
	Outcome          *string
	ResolutionNote   *string
	ResolvedByUserID *string
	ResolvedAt       *time.Time
	// ODRecordID is set ONLY on the response to an approval that created the
	// record, so the teacher's screen can say the slot was credited rather than
	// merely that the dispute was closed. It is not a stored dispute column.
	ODRecordID *string
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
	Outcome    string
	Note       string
	ResolvedAt time.Time
}

// CreditWrite credits the disputed slot. It carries no student, event, day or
// slot on purpose — those are read from the dispute row inside the query, so an
// approval cannot credit anything other than what was disputed.
type CreditWrite struct {
	Actor     Actor
	DisputeID string
	Note      string
}

type StudentReader interface {
	StudentByRollNo(ctx context.Context, rollNo string) (Student, error)
}

type Repository interface {
	Insert(ctx context.Context, tx pgx.Tx, write InsertWrite) (Record, error)
	OpenForSlot(ctx context.Context, studentID, eventID, odDate string, slot int) (Record, error)
	ListForAdmin(ctx context.Context, filter ListFilter) ([]Record, int, error)
	ListForTeacher(ctx context.Context, actorUserID string, filter ListFilter) ([]Record, int, error)
	// ForResolution reads one dispute. tx may be nil (pool read); passing the
	// transaction keeps the post-write read inside the same transaction, so a
	// resolve that COMMITTED can no longer be reported to the teacher as a
	// failure because a follow-up read happened to fail.
	ForResolution(ctx context.Context, tx pgx.Tx, actor Actor, disputeID string) (Record, error)
	// LockedStatus reads the dispute's status under `FOR UPDATE` inside tx. It
	// is what disambiguates "already resolved" (409) from "no such dispute"
	// (404) after a guarded UPDATE matched zero rows, without racing a
	// concurrent resolver between the update and the answer.
	LockedStatus(ctx context.Context, tx pgx.Tx, disputeID string) (string, bool, error)
	Resolve(ctx context.Context, tx pgx.Tx, write ResolveWrite) (int64, error)
	// CreditSlot writes the od_records row for an APPROVED dispute, inside the
	// resolving transaction. `created` is false when the student was already
	// marked for that slot — a moot approval, not a failed one.
	CreditSlot(ctx context.Context, tx pgx.Tx, write CreditWrite) (recordID string, created bool, err error)
}

type AuditStore interface {
	WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error
	// Audit writes an ADDITIONAL audit row inside a transaction WithAudit already
	// opened. Approving a dispute is two auditable facts — the decision and the
	// attendance record it created — and an od_records row with no od.created row
	// behind it is exactly the gap the audit ledger exists to close.
	Audit(ctx context.Context, tx pgx.Tx, evt store.AuditEvent) error
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
	// PayloadFunc ONLY — store.WithAudit prefers it whenever it is non-nil, so
	// also setting Payload left one of the two permanently dead. It runs after
	// the insert, which is what lets the payload carry the row's own id and
	// server timestamp: FR51 requires the form contents, the selected
	// event/category/slot AND the timestamp, and the timestamp is not knowable
	// before the write.
	evt := store.AuditEvent{
		Action:       domain.ActionDisputeSubmitted,
		ActorAccount: &email,
		ActorRole:    &actorRole,
		TargetTable:  &targetTable,
		TargetID:     &targetID,
		PayloadFunc: func() ([]byte, error) {
			return submitPayload(write, inserted), nil
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

	// No defaulting here. This block used to mint a random uuid when the
	// repository returned an empty record, which would hand the student a
	// reference matching no row while the audit row's target_id stayed "" — a
	// loud bug converted into a silent one, on the single handle a student has
	// for a dispute they cannot look up.
	if inserted.DisputeID == "" {
		return Receipt{}, domain.DomainError{
			Code:    contracts.ErrorCodeINTERNAL,
			Message: "The submission could not be recorded. Please try again.",
		}
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

// Outcome values. A resolution must say which one it is: wiring resolution to
// the record without this would credit every dispute, including the ones a
// teacher resolves BECAUSE the student was not there.
const (
	OutcomeApproved = "approved"
	OutcomeRejected = "rejected"
)

// Resolve closes an open dispute, and — when the outcome is APPROVED — credits
// the disputed slot in od_records in the same transaction.
//
// WHY THE WRITE LIVES HERE. Resolution used to be an annotation and nothing
// else: the teacher agreed the student had been missed, wrote a note, and the
// student still did not appear in od_records or on the public dashboard.
// Crediting the slot was a separate trip to the record-management surface that
// nothing prompted and nothing tracked, so in practice it did not happen and the
// student stayed missing from the day they had disputed.
//
// Both facts now move together or neither does: the dispute row, the od_records
// row and both audit rows are one transaction, so a teacher can never be shown
// "resolved" over a slot that was not credited.
func (s *Service) Resolve(ctx context.Context, actor Actor, disputeID, note, outcome string) (Record, error) {
	if actor.Role != "teacher" && actor.Role != "admin" {
		return Record{}, forbidden()
	}
	// The resolver is recorded on the row and in the audit ledger. An unparseable
	// or blank user id silently becomes NULL in pgtype (uuidParam swallows the
	// parse error), and disputes_resolved_state_ck does NOT require
	// resolved_by_user_id — so a malformed principal could resolve a dispute
	// with no resolver attached and pass every constraint.
	if _, err := uuid.Parse(strings.TrimSpace(actor.UserID)); err != nil {
		return Record{}, forbidden()
	}
	if strings.TrimSpace(disputeID) == "" {
		return Record{}, validation("dispute_id", "required", "Dispute id is required.")
	}
	if _, err := uuid.Parse(strings.TrimSpace(disputeID)); err != nil {
		return Record{}, validation("dispute_id", "invalid_format", "Dispute id must be a UUID.")
	}
	trimmedNote := strings.TrimSpace(note)
	if trimmedNote == "" || !hasVisibleCharacter(trimmedNote) {
		return Record{}, validation("resolution_note", "required", "Resolution note is required.")
	}
	if utf8.RuneCountInString(trimmedNote) > 2000 {
		return Record{}, validation("resolution_note", "too_long", "Resolution note must be at most 2000 characters.")
	}
	if hasControlCharacter(trimmedNote) {
		return Record{}, validation("resolution_note", "invalid_characters", "Resolution note must not contain control characters.")
	}
	trimmedOutcome := strings.TrimSpace(outcome)
	// No default. An absent outcome is ambiguous in the one direction that
	// matters — defaulting to approved credits slots nobody approved, defaulting
	// to rejected silently drops the credit the teacher meant to give — so it is
	// refused and the caller has to say.
	if trimmedOutcome != OutcomeApproved && trimmedOutcome != OutcomeRejected {
		return Record{}, validation("outcome", "invalid_value", "Outcome must be approved or rejected.")
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
		"outcome":         trimmedOutcome,
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

	var (
		changed      bool
		resolved     Record
		lockedStatus string
		lockedFound  bool
		creditedID   string
	)
	err := s.audited.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		rows, err := s.repo.Resolve(ctx, tx, ResolveWrite{
			Actor:      actor,
			DisputeID:  targetID,
			Outcome:    trimmedOutcome,
			Note:       trimmedNote,
			ResolvedAt: resolvedAt,
		})
		if err != nil {
			return false, err
		}
		if rows == 0 {
			// The UPDATE is guarded on status='open', so zero rows means either
			// already-resolved or gone. Read it back under the row lock, in this
			// transaction, rather than guessing or re-reading after commit.
			lockedStatus, lockedFound, err = s.repo.LockedStatus(ctx, tx, targetID)
			if err != nil {
				return false, err
			}
			return false, nil
		}
		// THE APPROVAL WRITES THE RECORD, here, in this transaction. Any failure
		// below rolls the resolution back with it, so the teacher is never told a
		// dispute was resolved over a slot that was not credited.
		if trimmedOutcome == OutcomeApproved {
			recordID, created, err := s.repo.CreditSlot(ctx, tx, CreditWrite{
				Actor:     actor,
				DisputeID: targetID,
				Note:      trimmedNote,
			})
			if err != nil {
				return false, err
			}
			if created {
				creditedID = recordID
				// The SECOND auditable fact. An od_records row that appeared with
				// no od.created behind it is precisely the gap the ledger exists to
				// close, and the dispute.resolved row above is about the decision,
				// not about the attendance record the decision produced.
				recordTable := "od_records"
				recordPayload, _ := json.Marshal(map[string]any{
					"source":     "dispute",
					"dispute_id": targetID,
					"reason":     trimmedNote,
				})
				if err := s.audited.Audit(ctx, tx, store.AuditEvent{
					Action:       domain.ActionODCreated,
					ActorAccount: &actorAccount,
					ActorRole:    &actorRole,
					TargetTable:  &recordTable,
					TargetID:     &recordID,
					Payload:      recordPayload,
				}); err != nil {
					return false, err
				}
			}
		}
		// Read the resolved row inside the SAME transaction that wrote it.
		record, err := s.repo.ForResolution(ctx, tx, actor, targetID)
		if err != nil {
			return false, err
		}
		resolved = record
		changed = true
		return true, nil
	})
	if err != nil {
		return Record{}, mapDisputePgError(err)
	}
	if !changed {
		if lockedFound && lockedStatus == "resolved" {
			return Record{}, disputeAlreadyResolved()
		}
		return Record{}, disputeNotFound()
	}
	if creditedID != "" {
		resolved.ODRecordID = &creditedID
	}
	return resolved, nil
}

// hasControlCharacter reports whether v carries a C0/C1 control character. A NUL
// is the sharp case: Postgres rejects U+0000 in `text` with SQLSTATE 22021 from
// INSIDE the insert, which would reach an anonymous caller as a 500 for input
// they fully control. service/event/category.go rejects these for exactly this
// reason; this is the same guard on the public write.
func hasControlCharacter(v string) bool {
	for _, r := range v {
		if r != '\n' && r != '\r' && r != '\t' && unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// hasVisibleCharacter reports whether v contains anything a human can see.
// strings.TrimSpace does NOT strip U+200B (zero-width space), U+2060 or U+FEFF,
// so a note made only of those passes a `!= ""` check, satisfies the database's
// `length BETWEEN 1 AND 2000`, and resolves a dispute with a blank decision.
func hasVisibleCharacter(v string) bool {
	for _, r := range v {
		// U+200B, U+2060 and U+FEFF are all General_Category=Cf, so the Cf test
		// covers every zero-width form strings.TrimSpace leaves behind.
		if unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		return true
	}
	return false
}

func validateSubmitInput(input SubmitInput, now time.Time) error {
	// Ordered slices, not maps: Go randomises map iteration order, so a body
	// missing two required fields named a different one on each run. 0.4's
	// review already landed "make required-field errors deterministic" once.
	for _, required := range []struct{ field, value string }{
		{"student_id", input.StudentID},
		{"submitted_email", input.SubmittedEmail},
		{"event_id", input.EventID},
		{"category_id", input.CategoryID},
		{"od_date", input.ODDate},
	} {
		if strings.TrimSpace(required.value) == "" {
			return validation(required.field, "required", required.field+" is required.")
		}
	}
	for _, id := range []struct{ field, value string }{
		{"student_id", input.StudentID},
		{"event_id", input.EventID},
		{"category_id", input.CategoryID},
	} {
		parsed, err := uuid.Parse(strings.TrimSpace(id.value))
		if err != nil {
			return validation(id.field, "invalid_format", id.field+" must be a UUID.")
		}
		// The nil UUID parses cleanly and then reaches pgtype as NULL, which is
		// how an absent id used to surface as EVENT_NOT_FOUND instead of a typed
		// validation failure.
		if parsed == uuid.Nil {
			return validation(id.field, "invalid_format", id.field+" must be a non-nil UUID.")
		}
	}
	if input.Slot < 1 || input.Slot > 20 {
		return validation("slot", "out_of_range", "Slot must be between 1 and 20.")
	}
	if input.ClaimedCoordinatorName == nil || strings.TrimSpace(*input.ClaimedCoordinatorName) == "" || !hasVisibleCharacter(*input.ClaimedCoordinatorName) {
		return validation("claimed_coordinator_name", "required", "Coordinator name is required.")
	}
	if input.StudentNote == nil || strings.TrimSpace(*input.StudentNote) == "" || !hasVisibleCharacter(*input.StudentNote) {
		return validation("student_note", "required", "Student note is required.")
	}
	// Counted in RUNES, not bytes. The contract's maxLength, the database CHECK
	// (`length(...)`) and the browser's maxLength all count characters, so a
	// byte count refused a 400-character Malayalam note as "at most 1000
	// characters". PublicAttendanceScreen.tsx's runeCount() helper documents
	// this as "the unit the Go service bounds on" — now true again.
	if utf8.RuneCountInString(strings.TrimSpace(input.SubmittedEmail)) > 320 {
		return validation("submitted_email", "too_long", "Email must be at most 320 characters.")
	}
	if hasControlCharacter(input.SubmittedEmail) {
		return validation("submitted_email", "invalid_characters", "Email must not contain control characters.")
	}
	note := strings.TrimSpace(*input.StudentNote)
	if utf8.RuneCountInString(note) > 1000 {
		return validation("student_note", "too_long", "Student note must be at most 1000 characters.")
	}
	if hasControlCharacter(note) {
		return validation("student_note", "invalid_characters", "Student note must not contain control characters.")
	}
	name := strings.TrimSpace(*input.ClaimedCoordinatorName)
	if utf8.RuneCountInString(name) > 200 {
		return validation("claimed_coordinator_name", "too_long", "Coordinator name must be at most 200 characters.")
	}
	if hasControlCharacter(name) {
		return validation("claimed_coordinator_name", "invalid_characters", "Coordinator name must not contain control characters.")
	}
	odDate, err := time.Parse("2006-01-02", strings.TrimSpace(input.ODDate))
	if err != nil {
		return validation("od_date", "invalid_format", "OD date must be YYYY-MM-DD.")
	}
	// This is a plausibility bound, NOT the event-range check: the range is a
	// database constraint (T22) because the day arrives from an anonymous
	// submitter and a handler check is not a guarantee. A day more than 24h
	// ahead cannot belong to any event that has already happened, so it is worth
	// refusing before it costs a transaction.
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

// submitPayload builds the FR51 audit payload: the form contents, the selected
// event/category/slot, and the timestamp. `inserted` is the row the write just
// produced, so dispute_id and submitted_at are the row's own values rather than
// anything re-derived.
func submitPayload(write InsertWrite, inserted Record) []byte {
	payload, _ := json.Marshal(map[string]any{
		"dispute_id":               inserted.DisputeID,
		"student_id":               write.StudentID,
		"submitted_email":          write.SubmittedEmail,
		"event_id":                 write.EventID,
		"category_id":              write.CategoryID,
		"od_date":                  write.ODDate,
		"slot":                     write.Slot,
		"claimed_coordinator_name": write.ClaimedCoordinatorName,
		"student_note":             write.StudentNote,
		"submitted_at":             inserted.SubmittedAt.UTC().Format(time.RFC3339Nano),
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

func eventArchived() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeEVENTARCHIVED, Message: "The event is archived."}
}

func mapDisputePgError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "55000" {
			return eventArchived()
		}
	}
	return err
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
