package event

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

type coordinatorAssignmentRejection struct {
	name       string
	email      string
	eventState string
	userRole   string
	wantCode   string
	wantField  string
	wantDetail string
	wantStatus int
}

var coordinatorAssignmentRejections = []coordinatorAssignmentRejection{
	{
		name:  "blank email is rejected before event lookup",
		email: "   ", eventState: "active", userRole: "student",
		wantCode: "VALIDATION_FAILED", wantField: "email", wantDetail: "required", wantStatus: 400,
	},
	{
		name:  "email longer than 320 runes is rejected before event lookup",
		email: "coordinator-" + longLocalPart(310) + "@amrita.edu", eventState: "active", userRole: "student",
		wantCode: "VALIDATION_FAILED", wantField: "email", wantDetail: "maximum", wantStatus: 400,
	},
	{
		name:  "malformed event id is a field error",
		email: "candidate@amrita.edu", eventState: "malformed-id", userRole: "student",
		wantCode: "VALIDATION_FAILED", wantField: "event_id", wantDetail: "invalid_format", wantStatus: 400,
	},
	{
		name:  "unknown event is not found",
		email: "candidate@amrita.edu", eventState: "missing", userRole: "student",
		wantCode: "EVENT_NOT_FOUND", wantStatus: 404,
	},
	{
		name:  "archived event is closed to new assignments",
		email: "candidate@amrita.edu", eventState: "archived", userRole: "student",
		wantCode: "EVENT_ARCHIVED", wantStatus: 409,
	},
	{
		name:  "unknown provisioned user is not found",
		email: "missing@amrita.edu", eventState: "active", userRole: "missing",
		wantCode: "USER_NOT_FOUND", wantStatus: 404,
	},
	{
		name:  "admin cannot be assigned as coordinator",
		email: "admin@amrita.edu", eventState: "active", userRole: "admin",
		wantCode: "ROLE_ALREADY_ASSIGNED", wantStatus: 409,
	},
	{
		name:  "teacher cannot be assigned as coordinator",
		email: "teacher@amrita.edu", eventState: "active", userRole: "teacher",
		wantCode: "ROLE_ALREADY_ASSIGNED", wantStatus: 409,
	},
	{
		name:  "duplicate courtesy check is a state conflict",
		email: "coordinator@amrita.edu", eventState: "active", userRole: "coordinator",
		wantCode: "COORDINATOR_ASSIGNMENT_EXISTS", wantStatus: 409,
	},
	{
		name:  "duplicate unique-index race is the same state conflict",
		email: "raced@amrita.edu", eventState: "active", userRole: "student",
		wantCode: "COORDINATOR_ASSIGNMENT_EXISTS", wantStatus: 409,
	},
}

func TestCoordinatorAssignmentRejectionMatrixIsInternallyCoherent(t *testing.T) {
	seenDuplicate := 0
	wantOrder := []string{
		"blank email is rejected before event lookup",
		"email longer than 320 runes is rejected before event lookup",
		"malformed event id is a field error",
		"unknown event is not found",
		"archived event is closed to new assignments",
		"unknown provisioned user is not found",
		"admin cannot be assigned as coordinator",
		"teacher cannot be assigned as coordinator",
		"duplicate courtesy check is a state conflict",
		"duplicate unique-index race is the same state conflict",
	}
	for index, row := range coordinatorAssignmentRejections {
		if row.name != wantOrder[index] {
			t.Fatalf("rejection order[%d]=%q, want %q", index, row.name, wantOrder[index])
		}
		if row.wantStatus < 400 || row.wantStatus >= 500 {
			t.Fatalf("%s status=%d, want typed client rejection", row.name, row.wantStatus)
		}
		if row.wantCode == "VALIDATION_FAILED" && (row.wantField == "" || row.wantDetail == "") {
			t.Fatalf("%s is field validation but has field=%q detail=%q", row.name, row.wantField, row.wantDetail)
		}
		if row.wantCode != "VALIDATION_FAILED" && (row.wantField != "" || row.wantDetail != "") {
			t.Fatalf("%s has non-validation code %s but field detail %q/%q", row.name, row.wantCode, row.wantField, row.wantDetail)
		}
		if row.wantCode == "COORDINATOR_ASSIGNMENT_EXISTS" {
			seenDuplicate++
			if row.wantStatus != 409 {
				t.Fatalf("%s status=%d, want 409", row.name, row.wantStatus)
			}
		}
	}
	if seenDuplicate != 2 {
		t.Fatalf("COORDINATOR_ASSIGNMENT_EXISTS appears %d times, want courtesy check and raced insert", seenDuplicate)
	}
}

func TestCoordinatorAssignmentCreateRejectsEveryInvalidRequestBeforeOpeningATransaction(t *testing.T) {
	for _, row := range coordinatorAssignmentRejections {
		t.Run(row.name, func(t *testing.T) {
			eventID := eventIDFor(row.eventState)
			reader := &fakeCoordinatorReader{
				status:    StatusActive,
				candidate: CoordinatorCandidate{ID: coordinatorUserID, Email: row.email, Role: row.userRole},
			}
			switch row.eventState {
			case "missing":
				reader.statusErr = ErrEventNotFound
			case "archived":
				reader.status = StatusArchived
			}
			if row.userRole == "missing" {
				reader.candidateErr = ErrCoordinatorUserNotFound
			}
			if strings.Contains(row.name, "duplicate courtesy") {
				reader.exists = true
			}
			writer := &fakeCoordinatorWriter{}
			if strings.Contains(row.name, "unique-index race") {
				writer.err = &pgconn.PgError{Code: "23505", ConstraintName: coordinatorAssignmentUniqueConstraint}
			}
			audit := &fakeAuditStore{}

			_, err := NewService(reader, audit, writer).CreateCoordinatorAssignment(
				context.Background(),
				adminActor(),
				CreateCoordinatorAssignmentInput{EventID: eventID, Email: row.email, CategoryIDs: coordinatorCategoryIDs()},
			)
			if err == nil {
				t.Fatal("CreateCoordinatorAssignment succeeded, want rejection")
			}
			var de domain.DomainError
			if !errors.As(err, &de) || string(de.Code) != row.wantCode {
				t.Fatalf("error = %#v, want code %s", err, row.wantCode)
			}
			if domain.HTTPStatus(de.Code) != row.wantStatus {
				t.Fatalf("status = %d, want %d", domain.HTTPStatus(de.Code), row.wantStatus)
			}
			if row.wantField != "" {
				if len(de.Details) != 1 || de.Details[0].Field != row.wantField || de.Details[0].Code != row.wantDetail {
					t.Fatalf("details = %#v, want %s/%s", de.Details, row.wantField, row.wantDetail)
				}
			}
			if !strings.Contains(row.name, "unique-index race") && audit.calls != 0 {
				t.Fatalf("WithAudit entered %d times before validation completed", audit.calls)
			}
		})
	}
}

func TestCoordinatorAssignmentCreateElevatesStudentAndAuditsOneAssignment(t *testing.T) {
	eventID := uuid.MustParse(activeEventID)
	assignmentID := uuid.NewString()
	createdAt := time.Date(2026, 8, 3, 4, 0, 0, 0, time.UTC)
	reader := &fakeCoordinatorReader{
		status:    StatusActive,
		candidate: CoordinatorCandidate{ID: coordinatorUserID, Email: "precheck@amrita.edu", Role: "student"},
	}
	writer := &fakeCoordinatorWriter{assignment: CoordinatorAssignment{
		ID: assignmentID, UserID: coordinatorUserID, Email: "locked@amrita.edu", EventID: eventID.String(), CreatedAt: createdAt,
	}}
	audit := &fakeAuditStore{}
	created, err := NewService(reader, audit, writer).CreateCoordinatorAssignment(
		context.Background(),
		adminActor(),
		CreateCoordinatorAssignmentInput{EventID: eventID.String(), Email: " candidate@amrita.edu ", CategoryIDs: coordinatorCategoryIDs()},
	)
	if err != nil {
		t.Fatalf("CreateCoordinatorAssignment = %v", err)
	}
	if created.ID != assignmentID || created.UserID != coordinatorUserID {
		t.Fatalf("created = %#v, want writer-returned assignment", created)
	}
	if audit.calls != 1 || !audit.changed {
		t.Fatalf("audit calls/changed = %d/%v, want exactly one changed audit", audit.calls, audit.changed)
	}
	if audit.event.Action != domain.ActionCoordinatorAssigned {
		t.Fatalf("audit action = %q", audit.event.Action)
	}
	if audit.event.TargetTable == nil || *audit.event.TargetTable != "event_coordinator_assignments" {
		t.Fatalf("target table = %#v", audit.event.TargetTable)
	}
	if audit.event.TargetID == nil || *audit.event.TargetID != assignmentID {
		t.Fatalf("target id = %#v, want assignment id", audit.event.TargetID)
	}
	var payload map[string]string
	if err := json.Unmarshal(audit.event.Payload, &payload); err != nil {
		t.Fatalf("decode audit payload: %v", err)
	}
	if payload["user_id"] != coordinatorUserID || payload["event_id"] != eventID.String() || payload["email"] != "locked@amrita.edu" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestCoordinatorAssignmentCreateAllowsExistingCoordinatorOnAnotherEvent(t *testing.T) {
	reader := &fakeCoordinatorReader{
		status:    StatusActive,
		candidate: CoordinatorCandidate{ID: coordinatorUserID, Email: "coord@amrita.edu", Role: "coordinator"},
	}
	writer := &fakeCoordinatorWriter{assignment: CoordinatorAssignment{
		ID: uuid.NewString(), UserID: coordinatorUserID, Email: "coord@amrita.edu", EventID: activeEventID, CreatedAt: time.Now(),
	}}
	if _, err := NewService(reader, &fakeAuditStore{}, writer).CreateCoordinatorAssignment(
		context.Background(), adminActor(), CreateCoordinatorAssignmentInput{EventID: activeEventID, Email: "coord@amrita.edu", CategoryIDs: coordinatorCategoryIDs()},
	); err != nil {
		t.Fatalf("existing coordinator assignment rejected: %v", err)
	}
	if writer.calls != 1 {
		t.Fatalf("writer calls = %d, want one assignment insert", writer.calls)
	}
}

func TestCoordinatorAssignmentCreateMapsDuplicateRaceWithoutWritingAudit(t *testing.T) {
	reader := &fakeCoordinatorReader{
		status:    StatusActive,
		candidate: CoordinatorCandidate{ID: coordinatorUserID, Email: "raced@amrita.edu", Role: "student"},
	}
	writer := &fakeCoordinatorWriter{err: &pgconn.PgError{Code: "23505", ConstraintName: coordinatorAssignmentUniqueConstraint}}
	_, err := NewService(reader, &fakeAuditStore{}, writer).CreateCoordinatorAssignment(
		context.Background(), adminActor(), CreateCoordinatorAssignmentInput{EventID: activeEventID, Email: "raced@amrita.edu", CategoryIDs: coordinatorCategoryIDs()},
	)
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeCOORDINATORASSIGNMENTEXISTS {
		t.Fatalf("error = %#v, want COORDINATOR_ASSIGNMENT_EXISTS", err)
	}
}

func TestCoordinatorAssignmentWriteErrorMapsOnlyTargetUserFKToUserNotFound(t *testing.T) {
	targetErr := mapCoordinatorAssignmentWriteError(&pgconn.PgError{
		Code:           "23503",
		ConstraintName: coordinatorAssignmentUserFK,
	})
	var targetDomain domain.DomainError
	if !errors.As(targetErr, &targetDomain) || targetDomain.Code != contracts.ErrorCodeUSERNOTFOUND {
		t.Fatalf("target user FK error = %#v, want USER_NOT_FOUND", targetErr)
	}

	actorErr := mapCoordinatorAssignmentWriteError(&pgconn.PgError{
		Code:           "23503",
		ConstraintName: coordinatorAssignmentCreatedByFK,
	})
	var actorDomain domain.DomainError
	if errors.As(actorErr, &actorDomain) {
		t.Fatalf("actor FK error = %#v, want internal/raw error", actorErr)
	}
}

func TestCoordinatorAssignmentListValidatesParentEventBeforeReturningRows(t *testing.T) {
	reader := &fakeCoordinatorReader{statusErr: ErrEventNotFound}
	_, err := NewService(reader, &fakeAuditStore{}, &fakeCoordinatorWriter{}).
		ListCoordinatorAssignments(context.Background(), adminActor(), missingEventID)
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeEVENTNOTFOUND {
		t.Fatalf("error = %#v, want EVENT_NOT_FOUND", err)
	}
	if reader.listAssignmentsCalls != 0 {
		t.Fatalf("listed assignments %d times before parent event validation", reader.listAssignmentsCalls)
	}
}

func longLocalPart(length int) string {
	value := make([]byte, length)
	for i := range value {
		value[i] = 'a'
	}
	return string(value)
}

const coordinatorUserID = "50000000-0000-4000-8000-000000000001"
const coordinatorCategoryID = "50000000-0000-4000-8000-000000000002"

func coordinatorCategoryIDs() []string { return []string{coordinatorCategoryID} }

type fakeCoordinatorReader struct {
	status               Status
	statusErr            error
	candidate            CoordinatorCandidate
	candidateErr         error
	exists               bool
	listAssignmentsCalls int
	assignments          []CoordinatorAssignment
}

func (f *fakeCoordinatorReader) ListBranches(context.Context) ([]Branch, error) { return nil, nil }
func (f *fakeCoordinatorReader) GetEvent(context.Context, uuid.UUID) (Event, error) {
	return Event{}, nil
}
func (f *fakeCoordinatorReader) ListEvents(context.Context, ListInput) ([]Event, int64, error) {
	return nil, 0, nil
}
func (f *fakeCoordinatorReader) EventStatus(context.Context, uuid.UUID) (Status, error) {
	if f.statusErr != nil {
		if errors.Is(f.statusErr, ErrEventNotFound) {
			return "", ErrEventNotFound
		}
		return "", f.statusErr
	}
	if f.status == "" {
		return StatusActive, nil
	}
	return f.status, nil
}
func (f *fakeCoordinatorReader) ListCategories(context.Context, uuid.UUID) ([]Category, error) {
	return nil, nil
}
func (f *fakeCoordinatorReader) GetCategory(context.Context, uuid.UUID) (Category, error) {
	return Category{ID: coordinatorCategoryID, EventID: activeEventID, Name: "Procession"}, nil
}
func (f *fakeCoordinatorReader) ListCoordinatorAssignments(context.Context, uuid.UUID) ([]CoordinatorAssignment, error) {
	f.listAssignmentsCalls++
	return f.assignments, nil
}
func (f *fakeCoordinatorReader) GetCoordinatorAssignment(context.Context, uuid.UUID, uuid.UUID) (CoordinatorAssignment, error) {
	if len(f.assignments) > 0 {
		return f.assignments[0], nil
	}
	return CoordinatorAssignment{
		ID:        uuid.NewString(),
		UserID:    coordinatorUserID,
		Email:     "candidate@amrita.edu",
		EventID:   activeEventID,
		CreatedAt: time.Now(),
	}, nil
}
func (f *fakeCoordinatorReader) GetCoordinatorCandidateByEmail(context.Context, string) (CoordinatorCandidate, error) {
	if f.candidateErr != nil {
		return CoordinatorCandidate{}, f.candidateErr
	}
	return f.candidate, nil
}
func (f *fakeCoordinatorReader) CoordinatorAssignmentExists(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return f.exists, nil
}
func (f *fakeCoordinatorReader) ListAssignedActiveEvents(context.Context, uuid.UUID) ([]Event, error) {
	return nil, nil
}
func (f *fakeCoordinatorReader) CoordinatorAssignedToEvent(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}

type fakeCoordinatorWriter struct {
	assignment CoordinatorAssignment
	err        error
	calls      int
}

func (f *fakeCoordinatorWriter) Create(context.Context, pgx.Tx, CreateWrite) (uuid.UUID, bool, error) {
	return uuid.Nil, false, nil
}
func (f *fakeCoordinatorWriter) Archive(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}
func (f *fakeCoordinatorWriter) CreateCategory(context.Context, pgx.Tx, CreateCategoryWrite) (Category, bool, error) {
	return Category{}, false, nil
}
func (f *fakeCoordinatorWriter) CreateCoordinatorAssignment(context.Context, pgx.Tx, CreateCoordinatorAssignmentWrite) (CoordinatorAssignment, bool, error) {
	f.calls++
	if f.err != nil {
		return CoordinatorAssignment{}, false, f.err
	}
	if f.assignment.ID == "" {
		f.assignment = CoordinatorAssignment{
			ID:        uuid.NewString(),
			UserID:    coordinatorUserID,
			Email:     "candidate@amrita.edu",
			EventID:   activeEventID,
			CreatedAt: time.Now(),
		}
	}
	return f.assignment, true, nil
}
func (f *fakeCoordinatorWriter) ReplaceCoordinatorCategoryAccess(context.Context, pgx.Tx, ReplaceCoordinatorCategoryAccessWrite) (bool, error) {
	f.calls++
	return f.err == nil, f.err
}
