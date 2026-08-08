package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

type fakeReader struct {
	branches    []Branch
	event       Event
	events      []Event
	total       int64
	err         error
	listIn      ListInput
	creditUsage CaptureCreditUsage
}

func (f *fakeReader) ListBranches(context.Context) ([]Branch, error) { return f.branches, f.err }
func (f *fakeReader) GetEvent(context.Context, uuid.UUID) (Event, error) {
	return f.event, f.err
}
func (f *fakeReader) ListEvents(_ context.Context, input ListInput) ([]Event, int64, error) {
	f.listIn = input
	return f.events, f.total, f.err
}
func (f *fakeReader) EventStatus(context.Context, uuid.UUID) (Status, error) {
	return f.event.Status, f.err
}
func (f *fakeReader) ListCategories(context.Context, uuid.UUID) ([]Category, error) {
	return nil, f.err
}
func (f *fakeReader) ListCoordinatorAssignments(context.Context, uuid.UUID) ([]CoordinatorAssignment, error) {
	return nil, f.err
}
func (f *fakeReader) GetCoordinatorCandidateByEmail(context.Context, string) (CoordinatorCandidate, error) {
	return CoordinatorCandidate{}, f.err
}
func (f *fakeReader) CoordinatorAssignmentExists(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, f.err
}
func (f *fakeReader) ListAssignedActiveEvents(context.Context, uuid.UUID) ([]Event, error) {
	return nil, f.err
}
func (f *fakeReader) CoordinatorAssignedToEvent(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, f.err
}
func (f *fakeReader) GetCaptureCreditConfig(context.Context, uuid.UUID) (CaptureCreditConfig, error) {
	return CaptureCreditConfig{}, f.err
}
func (f *fakeReader) ListCaptureCreditUsageByEvent(context.Context, uuid.UUID, time.Time) ([]CaptureCreditUsage, error) {
	return nil, f.err
}
func (f *fakeReader) GetCaptureCreditUsageForCoordinator(context.Context, uuid.UUID, uuid.UUID, time.Time) (CaptureCreditUsage, error) {
	return f.creditUsage, f.err
}

type fakeWriter struct {
	createID            uuid.UUID
	createChanged       bool
	archiveChange       bool
	err                 error
	createInput         CreateWrite
	archiveCalls        int
	captureConfigResult CaptureCreditConfigWriteResult
	captureConfigCalls  int
	captureConfigInput  CaptureCreditConfigWrite
	captureResetResult  CaptureCreditResetWriteResult
	captureResetCalls   int
	captureResetInput   CaptureCreditResetWrite
}

func (f *fakeWriter) Create(_ context.Context, _ pgx.Tx, input CreateWrite) (uuid.UUID, bool, error) {
	f.createInput = input
	return f.createID, f.createChanged, f.err
}
func (f *fakeWriter) Archive(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (bool, error) {
	f.archiveCalls++
	return f.archiveChange, f.err
}
func (f *fakeWriter) CreateCategory(context.Context, pgx.Tx, CreateCategoryWrite) (Category, bool, error) {
	return Category{}, false, f.err
}
func (f *fakeWriter) CreateCoordinatorAssignment(context.Context, pgx.Tx, CreateCoordinatorAssignmentWrite) (CoordinatorAssignment, bool, error) {
	return CoordinatorAssignment{}, false, f.err
}
func (f *fakeWriter) UpsertCaptureCreditConfig(_ context.Context, _ pgx.Tx, input CaptureCreditConfigWrite) (CaptureCreditConfigWriteResult, bool, error) {
	f.captureConfigCalls++
	f.captureConfigInput = input
	return f.captureConfigResult, true, f.err
}
func (f *fakeWriter) ResetCaptureCreditUsage(_ context.Context, _ pgx.Tx, input CaptureCreditResetWrite) (CaptureCreditResetWriteResult, bool, error) {
	f.captureResetCalls++
	f.captureResetInput = input
	return f.captureResetResult, true, f.err
}

type fakeAuditStore struct {
	event   store.AuditEvent
	changed bool
	calls   int
	err     error
}

func (f *fakeAuditStore) WithAudit(ctx context.Context, event store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error {
	f.calls++
	f.event = event
	changed, err := write(ctx, nil)
	f.changed = changed
	if err != nil {
		return err
	}
	if changed && event.PayloadFunc != nil {
		payload, payloadErr := event.PayloadFunc()
		if payloadErr != nil {
			return payloadErr
		}
		f.event.Payload = payload
	}
	return f.err
}

func TestCreateValidatesBeforeWrite(t *testing.T) {
	branchID := uuid.New()
	cases := []struct {
		name  string
		input CreateInput
		field string
		code  string
	}{
		{name: "blank name", input: CreateInput{EventDate: "2026-08-15", BranchIDs: []string{branchID.String()}}, field: "name", code: "required"},
		{name: "invalid date", input: CreateInput{Name: "Event", EventDate: "2026-02-31", BranchIDs: []string{branchID.String()}}, field: "event_date", code: "invalid_format"},
		{name: "no branches", input: CreateInput{Name: "Event", EventDate: "2026-08-15"}, field: "branch_ids", code: "required"},
		{name: "duplicate branches", input: CreateInput{Name: "Event", EventDate: "2026-08-15", BranchIDs: []string{branchID.String(), branchID.String()}}, field: "branch_ids", code: "duplicate"},
		{name: "malformed branch", input: CreateInput{Name: "Event", EventDate: "2026-08-15", BranchIDs: []string{"not-a-uuid"}}, field: "branch_ids[0]", code: "invalid_format"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			audit := &fakeAuditStore{}
			svc := NewService(&fakeReader{}, audit, &fakeWriter{})
			_, err := svc.Create(context.Background(), Actor{UserID: uuid.NewString(), Email: "admin@amrita.edu", Role: "admin"}, tc.input)
			var de domain.DomainError
			if !errors.As(err, &de) || de.Code != contracts.ErrorCodeVALIDATIONFAILED {
				t.Fatalf("error = %v, want VALIDATION_FAILED", err)
			}
			if len(de.Details) == 0 || de.Details[0].Field != tc.field || de.Details[0].Code != tc.code {
				t.Fatalf("details = %#v, want %s/%s", de.Details, tc.field, tc.code)
			}
			if audit.calls != 0 {
				t.Fatal("write boundary called for invalid input")
			}
		})
	}
}

func TestCreateRejectsUnknownBranchAtOriginalIndex(t *testing.T) {
	known := uuid.New()
	unknown := uuid.New()
	audit := &fakeAuditStore{}
	svc := NewService(&fakeReader{branches: []Branch{{ID: known.String()}}}, audit, &fakeWriter{})
	_, err := svc.Create(context.Background(), Actor{UserID: uuid.NewString(), Email: "admin@amrita.edu", Role: "admin"}, CreateInput{
		Name: "Event", EventDate: "2026-08-15", BranchIDs: []string{known.String(), unknown.String()},
	})
	var de domain.DomainError
	if !errors.As(err, &de) || len(de.Details) != 1 || de.Details[0].Field != "branch_ids[1]" || de.Details[0].Code != "not_found" {
		t.Fatalf("error = %#v, want branch_ids[1]/not_found", err)
	}
	if audit.calls != 0 {
		t.Fatal("write boundary called for unknown branch")
	}
}

func TestCreateRejectsMoreThanHundredBranchesBeforeReadOrWrite(t *testing.T) {
	reader := &fakeReader{}
	audit := &fakeAuditStore{}
	branchIDs := make([]string, 101)
	_, err := NewService(reader, audit, &fakeWriter{}).Create(
		context.Background(),
		Actor{UserID: uuid.NewString(), Email: "admin@amrita.edu", Role: "admin"},
		CreateInput{Name: "Event", EventDate: "2026-08-15", BranchIDs: branchIDs},
	)
	var de domain.DomainError
	if !errors.As(err, &de) || len(de.Details) != 1 || de.Details[0].Field != "branch_ids" || de.Details[0].Code != "maximum" {
		t.Fatalf("error=%#v, want branch_ids/maximum", err)
	}
	if audit.calls != 0 {
		t.Fatal("write boundary called for oversized branch list")
	}
}

func TestCreateTrimsInputAndAuditsStableContext(t *testing.T) {
	branchA, branchB, eventID := uuid.New(), uuid.New(), uuid.New()
	created := Event{ID: eventID.String(), Name: "Innovation Day", EventDate: "2026-08-15", EndDate: "2026-08-15", Status: StatusActive}
	reader := &fakeReader{branches: []Branch{{ID: branchA.String()}, {ID: branchB.String()}}, event: created}
	writer := &fakeWriter{createID: eventID, createChanged: true}
	audit := &fakeAuditStore{}
	svc := NewService(reader, audit, writer)

	got, err := svc.Create(context.Background(), Actor{UserID: uuid.NewString(), Email: "  ADMIN@amrita.edu ", Role: "admin"}, CreateInput{
		Name: "  Innovation Day  ", EventDate: "2026-08-15", BranchIDs: []string{branchA.String(), branchB.String()},
	})
	if err != nil || got.ID != eventID.String() {
		t.Fatalf("Create = %#v, %v", got, err)
	}
	if writer.createInput.Name != "Innovation Day" || len(writer.createInput.BranchIDs) != 2 {
		t.Errorf("normalized write = %#v", writer.createInput)
	}
	if !audit.changed || audit.event.Action != domain.ActionEventCreated || *audit.event.ActorAccount != "admin@amrita.edu" || *audit.event.TargetID != eventID.String() {
		t.Errorf("audit event = %#v changed=%v", audit.event, audit.changed)
	}
	var payload map[string]any
	if err := json.Unmarshal(audit.event.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["event_date"] != "2026-08-15" {
		t.Errorf("payload = %#v", payload)
	}
}

func TestArchiveIsIdempotentAndAuditsOnlyTransition(t *testing.T) {
	eventID := uuid.New()
	actorID := uuid.New()
	for _, tc := range []struct {
		name    string
		changed bool
	}{
		{name: "first transition", changed: true},
		{name: "repeat no-op", changed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &fakeReader{event: Event{ID: eventID.String(), Name: "Event", Status: StatusArchived, ArchivedAt: ptrTime(time.Now())}}
			writer := &fakeWriter{archiveChange: tc.changed}
			audit := &fakeAuditStore{}
			svc := NewService(reader, audit, writer)
			got, err := svc.Archive(context.Background(), Actor{UserID: actorID.String(), Email: "admin@amrita.edu", Role: "admin"}, eventID.String())
			if err != nil || got.Status != StatusArchived {
				t.Fatalf("Archive = %#v, %v", got, err)
			}
			if audit.changed != tc.changed {
				t.Errorf("audit changed = %v, want %v", audit.changed, tc.changed)
			}
			if audit.event.Action != domain.ActionEventArchived {
				t.Errorf("action = %q", audit.event.Action)
			}
		})
	}
}

func TestListAppliesDefaultsAndBounds(t *testing.T) {
	reader := &fakeReader{events: []Event{{ID: uuid.NewString()}}, total: 1}
	svc := NewService(reader, &fakeAuditStore{}, &fakeWriter{})
	page, err := svc.List(context.Background(), ListInput{})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if reader.listIn.Status != StatusAll || reader.listIn.Page != 1 || reader.listIn.PageSize != 20 {
		t.Errorf("defaults = %#v", reader.listIn)
	}
	if page.Total != 1 || len(page.Data) != 1 {
		t.Errorf("page = %#v", page)
	}
}

func TestListRejectsEveryInvalidBoundBeforeRead(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input ListInput
		field string
		code  string
	}{
		{name: "status", input: ListInput{Status: "deleted"}, field: "status", code: "invalid_enum"},
		{name: "page", input: ListInput{Page: -1}, field: "page", code: "minimum"},
		{name: "page size minimum", input: ListInput{PageSize: -1}, field: "page_size", code: "minimum"},
		{name: "page size maximum", input: ListInput{PageSize: 101}, field: "page_size", code: "maximum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &fakeReader{}
			_, err := NewService(reader, &fakeAuditStore{}, &fakeWriter{}).List(context.Background(), tc.input)
			var de domain.DomainError
			if !errors.As(err, &de) || len(de.Details) != 1 || de.Details[0].Field != tc.field || de.Details[0].Code != tc.code {
				t.Fatalf("error=%#v", err)
			}
		})
	}
}

func TestUpdateCaptureCreditConfigRejectsOverflowBeforeWrite(t *testing.T) {
	eventID, actorID := uuid.New(), uuid.New()
	writer := &fakeWriter{}
	audit := &fakeAuditStore{}
	_, err := NewService(&fakeReader{event: Event{Status: StatusActive}}, audit, writer).UpdateCaptureCreditConfig(
		context.Background(),
		Actor{UserID: actorID.String(), Email: "admin@amrita.edu", Role: "admin"},
		CaptureCreditConfigInput{EventID: eventID.String(), Limit: maxCaptureCreditLimit + 1},
	)
	var de domain.DomainError
	if !errors.As(err, &de) || len(de.Details) != 1 || de.Details[0].Field != "limit" || de.Details[0].Code != "maximum" {
		t.Fatalf("error=%#v, want limit maximum validation", err)
	}
	if audit.calls != 0 || writer.captureConfigCalls != 0 {
		t.Fatalf("audit/write calls = %d/%d, want none", audit.calls, writer.captureConfigCalls)
	}
}

func TestUpdateCaptureCreditConfigAuditUsesTransactionPreviousLimit(t *testing.T) {
	eventID, actorID := uuid.New(), uuid.New()
	writer := &fakeWriter{captureConfigResult: CaptureCreditConfigWriteResult{
		Config:        CaptureCreditConfig{EventID: eventID.String(), Limit: 8, UpdatedAt: time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC)},
		PreviousLimit: 3,
	}}
	audit := &fakeAuditStore{}
	updated, err := NewService(&fakeReader{event: Event{Status: StatusActive}}, audit, writer).UpdateCaptureCreditConfig(
		context.Background(),
		Actor{UserID: actorID.String(), Email: " ADMIN@amrita.edu ", Role: "admin"},
		CaptureCreditConfigInput{EventID: eventID.String(), Limit: 8},
	)
	if err != nil {
		t.Fatalf("UpdateCaptureCreditConfig: %v", err)
	}
	if updated.Limit != 8 || writer.captureConfigInput.Limit != 8 {
		t.Fatalf("updated/input = %#v/%#v", updated, writer.captureConfigInput)
	}
	var payload map[string]any
	if err := json.Unmarshal(audit.event.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["previous_limit"] != float64(3) || payload["new_limit"] != float64(8) {
		t.Fatalf("payload=%#v, want transaction previous and new limits", payload)
	}
}

func TestResetCaptureCreditsAuditUsesTransactionPreviousConsumed(t *testing.T) {
	eventID, actorID, userID := uuid.New(), uuid.New(), uuid.New()
	writer := &fakeWriter{captureResetResult: CaptureCreditResetWriteResult{PreviousConsumed: 4}}
	reader := &fakeReader{
		event: Event{Status: StatusActive},
		creditUsage: CaptureCreditUsage{
			EventID: eventID.String(), UserID: userID.String(), Email: "coord@amrita.edu", Limit: 5,
		},
	}
	audit := &fakeAuditStore{}
	_, err := NewService(reader, audit, writer).ResetCaptureCredits(
		context.Background(),
		Actor{UserID: actorID.String(), Email: "admin@amrita.edu", Role: "admin"},
		eventID.String(),
		userID.String(),
	)
	if err != nil {
		t.Fatalf("ResetCaptureCredits: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(audit.event.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["previous_consumed"] != float64(4) || payload["new_consumed"] != float64(0) {
		t.Fatalf("payload=%#v, want transaction previous consumed", payload)
	}
}

func TestCaptureCreditWriteErrorMapsWrappedCoordinatorMissing(t *testing.T) {
	err := mapCaptureCreditWriteError(fmt.Errorf("store: business write: %w", ErrCoordinatorUserNotFound))
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeUSERNOTFOUND {
		t.Fatalf("error=%v, want %s", err, contracts.ErrorCodeUSERNOTFOUND)
	}
}

func TestArchiveMapsMissingEventAndCarriesStableAuditContext(t *testing.T) {
	eventID, actorID := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name      string
		readerErr error
		wantCode  contracts.ErrorCode
	}{
		{name: "success"},
		{name: "missing", readerErr: ErrEventNotFound, wantCode: contracts.ErrorCodeEVENTNOTFOUND},
	} {
		t.Run(tc.name, func(t *testing.T) {
			audit := &fakeAuditStore{}
			reader := &fakeReader{event: Event{ID: eventID.String(), Status: StatusArchived}, err: tc.readerErr}
			_, err := NewService(reader, audit, &fakeWriter{archiveChange: tc.readerErr == nil}).Archive(
				context.Background(),
				Actor{UserID: actorID.String(), Email: " ADMIN@amrita.edu ", Role: "admin"},
				eventID.String(),
			)
			if tc.wantCode != "" {
				var de domain.DomainError
				if !errors.As(err, &de) || de.Code != tc.wantCode {
					t.Fatalf("error=%v, want %s", err, tc.wantCode)
				}
			} else if err != nil {
				t.Fatalf("Archive error=%v", err)
			}
			if audit.event.Action != domain.ActionEventArchived || *audit.event.ActorAccount != "admin@amrita.edu" || *audit.event.TargetID != eventID.String() {
				t.Fatalf("audit=%#v", audit.event)
			}
			var payload map[string]string
			if err := json.Unmarshal(audit.event.Payload, &payload); err != nil || payload["prior_status"] != "active" || payload["new_status"] != "archived" {
				t.Fatalf("payload=%#v err=%v", payload, err)
			}
		})
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
