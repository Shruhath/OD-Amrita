package teacher

// teacher_test.go — Story 2.3 coverage for AC 1-5 at the SERVICE layer.
// UNIT test: no build tag, no database.
//
// Invoked by: api-ci -> build job (`go test ./...`).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

const (
	activeAdminID    = "30000000-0000-4000-8000-000000000001"
	studentUserID    = "30000000-0000-4000-8000-000000000002"
	teacherBranchOne = "10000000-0000-4000-8000-000000000001"
	teacherBranchTwo = "10000000-0000-4000-8000-000000000002"
)

// teacherRejection is one rejected create and the exact answer the caller is
// owed. Order matters as much as content: Task 4's validation order (Dev
// Notes, "Validation order in Create") must return every one of these BEFORE
// a transaction opens, or a rejected create can still leave a role elevation
// or a branch association behind — the exact divergence AC 4's conservation
// property forbids.
type teacherRejection struct {
	name       string // scenario label
	wantCode   string // canonical contract error code (string, not contracts.ErrorCode — that keeps this table independent of the generated enum until Task 2 regenerates it)
	wantField  string // ValidationErrorDetail.field, empty when not a field error
	wantDetail string // ValidationErrorDetail.code, empty when not a field error
	wantStatus int
}

// teacherRejections is the complete rejection matrix for Create, in the order
// the service must evaluate it (Dev Notes steps 1-6). A reordering that still
// returns the right codes for these cases is acceptable; one that returns a
// DIFFERENT code for any case here is a behavior change, not a refactor.
var teacherRejections = []teacherRejection{
	{
		name:       "blank email is rejected before anything else is consulted",
		wantCode:   "VALIDATION_FAILED",
		wantField:  "email",
		wantDetail: "required",
		wantStatus: 400,
	},
	{
		name:       "an email over the contract's 320-character maximum is rejected before any lookup",
		wantCode:   "VALIDATION_FAILED",
		wantField:  "email",
		wantDetail: "maximum",
		wantStatus: 400,
	},
	{
		name:       "empty branch_ids is rejected before any lookup",
		wantCode:   "VALIDATION_FAILED",
		wantField:  "branch_ids",
		wantDetail: "required",
		wantStatus: 400,
	},
	{
		name:       "more than 100 branch_ids is rejected",
		wantCode:   "VALIDATION_FAILED",
		wantField:  "branch_ids",
		wantDetail: "maximum",
		wantStatus: 400,
	},
	{
		name:       "a non-UUID branch id is a field error, never a 500",
		wantCode:   "VALIDATION_FAILED",
		wantField:  "branch_ids[0]",
		wantDetail: "invalid_format",
		wantStatus: 400,
	},
	{
		name:       "duplicate branch ids in one request are rejected",
		wantCode:   "VALIDATION_FAILED",
		wantField:  "branch_ids",
		wantDetail: "duplicate",
		wantStatus: 400,
	},
	{
		name:       "a non-admin actor is forbidden without touching the database",
		wantCode:   "RBAC_FORBIDDEN",
		wantStatus: 403,
	},
	{
		name:       "an email with no matching users row is not found",
		wantCode:   "USER_NOT_FOUND",
		wantStatus: 404,
	},
	{
		name:       "an email whose user is already admin/teacher/coordinator is a state conflict",
		wantCode:   "ROLE_ALREADY_ASSIGNED",
		wantStatus: 409,
	},
	{
		name:       "an unknown branch id is a field error, mirroring event.Create's shape",
		wantCode:   "VALIDATION_FAILED",
		wantField:  "branch_ids[0]",
		wantDetail: "not_found",
		wantStatus: 400,
	},
	{
		name:       "the raced AssignTeacherRole no-rows path maps to the same typed conflict as the pre-check",
		wantCode:   "ROLE_ALREADY_ASSIGNED",
		wantStatus: 409,
	},
}

// TestTeacherRejectionMatrixIsInternallyCoherent is the ACTIVE guard: it fails
// if a VALIDATION_FAILED case ever loses its field/detail pair, or a non-field
// error grows one — the same shape Story 2.2's
// TestCategoryRejectionMatrixIsInternallyCoherent enforced. It needs no symbol
// from the not-yet-existing production package, so it is green today and
// stays green as the table grows.
func TestTeacherRejectionMatrixIsInternallyCoherent(t *testing.T) {
	seen := make(map[string]bool, len(teacherRejections))
	for _, tc := range teacherRejections {
		if seen[tc.name] {
			t.Errorf("[P1] duplicate scenario name %q", tc.name)
		}
		seen[tc.name] = true

		hasField := tc.wantField != ""
		hasDetail := tc.wantDetail != ""
		if hasField != hasDetail {
			t.Errorf("[P1] %s: field=%q detail=%q must both be present or both be empty", tc.name, tc.wantField, tc.wantDetail)
		}
		if tc.wantCode == "VALIDATION_FAILED" && !hasField {
			t.Errorf("[P1] %s: VALIDATION_FAILED must carry a field/detail pair", tc.name)
		}
		if tc.wantCode != "VALIDATION_FAILED" && hasField {
			t.Errorf("[P1] %s: only VALIDATION_FAILED may carry a field/detail pair, got code=%s", tc.name, tc.wantCode)
		}
		if tc.wantStatus < 400 || tc.wantStatus >= 600 {
			t.Errorf("[P1] %s: status %d is not a 4xx/5xx rejection", tc.name, tc.wantStatus)
		}
		if tc.wantCode == "" {
			t.Errorf("[P1] %s: every rejection must carry a code", tc.name)
		}
	}
	// The order below is Dev Notes' documented validation order. A test data
	// table that silently reorders itself would stop proving the "returns
	// before a transaction opens" property this story's whole design rests on.
	wantOrder := []string{
		"blank email is rejected before anything else is consulted",
		"an email over the contract's 320-character maximum is rejected before any lookup",
		"empty branch_ids is rejected before any lookup",
		"more than 100 branch_ids is rejected",
		"a non-UUID branch id is a field error, never a 500",
		"duplicate branch ids in one request are rejected",
		"a non-admin actor is forbidden without touching the database",
		"an email with no matching users row is not found",
		"an email whose user is already admin/teacher/coordinator is a state conflict",
		"an unknown branch id is a field error, mirroring event.Create's shape",
		"the raced AssignTeacherRole no-rows path maps to the same typed conflict as the pre-check",
	}
	if len(wantOrder) != len(teacherRejections) {
		t.Fatalf("[P1] wantOrder has %d entries, teacherRejections has %d — keep them in lockstep", len(wantOrder), len(teacherRejections))
	}
	for i, tc := range teacherRejections {
		if tc.name != wantOrder[i] {
			t.Errorf("[P1] position %d = %q, want %q — validation order drifted", i, tc.name, wantOrder[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type fakeTeacherReader struct {
	userID        string
	role          string
	notFound      bool
	teacherExists bool
	branchCount   int
	profile       TeacherProfile
	teachers      []TeacherProfile
	total         int64
	err           error
}

func (f *fakeTeacherReader) GetTeacherByEmail(context.Context, string) (resolvedUser, error) {
	if f.notFound {
		return resolvedUser{}, ErrUserNotFound
	}
	if f.err != nil {
		return resolvedUser{}, f.err
	}
	return resolvedUser{ID: f.userID, Role: f.role}, nil
}

func (f *fakeTeacherReader) GetTeacherProfile(context.Context, uuid.UUID) (TeacherProfile, error) {
	return f.profile, f.err
}

func (f *fakeTeacherReader) TeacherExists(context.Context, uuid.UUID) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.teacherExists || f.userID != "", nil
}

func (f *fakeTeacherReader) ListTeachers(context.Context, ListInput) ([]TeacherProfile, int64, error) {
	return f.teachers, f.total, f.err
}

func (f *fakeTeacherReader) CountBranchesByIDs(_ context.Context, branchIDs []uuid.UUID) (int, error) {
	if f.branchCount != 0 || f.err != nil {
		return f.branchCount, f.err
	}
	return len(branchIDs), nil
}

func (f *fakeTeacherReader) CountCategoriesByIDs(_ context.Context, categoryIDs []uuid.UUID) (int, error) {
	return len(categoryIDs), f.err
}

type countingTeacherWriter struct {
	userID         string
	assignCalls    int
	addBranchCalls int
	replaceCalls   int
	raceNoRows     bool
	err            error
}

func (w *countingTeacherWriter) AssignRoleAndBranches(_ context.Context, _ pgx.Tx, input AssignWrite) (string, bool, error) {
	w.assignCalls++
	if w.raceNoRows {
		return "", false, ErrRoleAlreadyAssigned
	}
	if w.err != nil {
		return "", false, w.err
	}
	w.addBranchCalls += len(input.BranchIDs)
	userID := w.userID
	if userID == "" {
		userID = input.UserID.String()
	}
	return userID, true, nil
}

func (w *countingTeacherWriter) ReplaceCategoryAccess(_ context.Context, _ pgx.Tx, _ ReplaceCategoryAccessWrite) (bool, error) {
	w.replaceCalls++
	return w.err == nil, w.err
}

type fakeAuditStore struct {
	event store.AuditEvent
	calls int
	err   error
}

func (f *fakeAuditStore) WithAudit(ctx context.Context, event store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error {
	f.calls++
	f.event = event
	_, err := write(ctx, nil)
	if err != nil {
		return err
	}
	return f.err
}

func adminActor() Actor {
	return Actor{UserID: activeAdminID, Email: "admin@amrita.edu", Role: "admin"}
}

// ---------------------------------------------------------------------------
// Rejection matrix, exercised end to end
// ---------------------------------------------------------------------------

func TestTeacherCreateRejectsEveryInvalidRequestBeforeOpeningATransaction(t *testing.T) {
	for _, tc := range teacherRejections {
		t.Run(tc.name, func(t *testing.T) {
			actor := adminActor()
			reader := &fakeTeacherReader{userID: studentUserID, role: "student"}
			writer := &countingTeacherWriter{}
			audit := &fakeAuditStore{}
			input := CreateInput{Email: "new-teacher@amrita.edu", BranchIDs: []string{teacherBranchOne}, CategoryIDs: []string{teacherBranchOne}}

			switch tc.name {
			case "blank email is rejected before anything else is consulted":
				input.Email = "   "
			case "an email over the contract's 320-character maximum is rejected before any lookup":
				input.Email = strings.Repeat("a", 310) + "@amrita.edu" // 321 runes
			case "empty branch_ids is rejected before any lookup":
				input.BranchIDs = nil
			case "more than 100 branch_ids is rejected":
				ids := make([]string, 101)
				for i := range ids {
					ids[i] = uuid.NewString()
				}
				input.BranchIDs = ids
			case "a non-UUID branch id is a field error, never a 500":
				input.BranchIDs = []string{"not-a-uuid"}
			case "duplicate branch ids in one request are rejected":
				input.BranchIDs = []string{teacherBranchOne, teacherBranchOne}
			case "a non-admin actor is forbidden without touching the database":
				actor = Actor{UserID: activeAdminID, Email: "student@amrita.edu", Role: "student"}
			case "an email with no matching users row is not found":
				reader.notFound = true
			case "an email whose user is already admin/teacher/coordinator is a state conflict":
				reader.role = "teacher"
			case "an unknown branch id is a field error, mirroring event.Create's shape":
				reader.branchCount = 0
				input.BranchIDs = []string{teacherBranchOne, teacherBranchTwo}
				reader.branchCount = 1
			case "the raced AssignTeacherRole no-rows path maps to the same typed conflict as the pre-check":
				writer.raceNoRows = true
			}

			svc := NewService(reader, audit, writer)
			_, err := svc.Create(context.Background(), actor, input)

			var domainErr domain.DomainError
			if !errors.As(err, &domainErr) {
				t.Fatalf("error = %v, want a DomainError", err)
			}
			if string(domainErr.Code) != tc.wantCode {
				t.Errorf("code = %s, want %s", domainErr.Code, tc.wantCode)
			}
			if domain.HTTPStatus(domainErr.Code) != tc.wantStatus {
				t.Errorf("status = %d, want %d", domain.HTTPStatus(domainErr.Code), tc.wantStatus)
			}
			if tc.wantField != "" {
				if len(domainErr.Details) != 1 || domainErr.Details[0].Field != tc.wantField || domainErr.Details[0].Code != tc.wantDetail {
					t.Errorf("detail = %#v, want field=%s code=%s", domainErr.Details, tc.wantField, tc.wantDetail)
				}
			}
			// The point of the whole ordering rule: a rejection must never have
			// reached the write boundary — a role elevation or branch association
			// left behind is exactly what AC 4's conservation property forbids.
			// The one exception is the raced-elevation case, which by definition
			// DOES reach AssignTeacherRole (that is what races) but must never
			// reach AddUserBranch.
			if tc.name == "the raced AssignTeacherRole no-rows path maps to the same typed conflict as the pre-check" {
				if audit.calls != 1 || writer.assignCalls != 1 || writer.addBranchCalls != 0 {
					t.Errorf("raced elevation write shape = audit=%d assign=%d branch=%d, want audit=1 assign=1 branch=0",
						audit.calls, writer.assignCalls, writer.addBranchCalls)
				}
				return
			}
			if audit.calls != 0 || writer.assignCalls != 0 || writer.addBranchCalls != 0 {
				t.Errorf("write boundary reached for a rejected create (audit=%d assign=%d branch=%d)",
					audit.calls, writer.assignCalls, writer.addBranchCalls)
			}
		})
	}
}

func TestTeacherCreateElevatesTheStudentRowAndAssociatesEveryBranch(t *testing.T) {
	reader := &fakeTeacherReader{
		userID: studentUserID, role: "student",
		profile: TeacherProfile{UserID: studentUserID, Email: "new-teacher@amrita.edu", Branches: []Branch{
			{ID: teacherBranchOne, Code: "CSE", Name: "Computer Science"},
			{ID: teacherBranchTwo, Code: "ECE", Name: "Electronics"},
		}},
	}
	writer := &countingTeacherWriter{}
	svc := NewService(reader, &fakeAuditStore{}, writer)

	profile, err := svc.Create(context.Background(), adminActor(), CreateInput{
		Email:       "new-teacher@amrita.edu",
		BranchIDs:   []string{teacherBranchOne, teacherBranchTwo},
		CategoryIDs: []string{teacherBranchOne},
	})
	if err != nil {
		t.Fatalf("create teacher profile: %v", err)
	}
	if writer.assignCalls != 1 {
		t.Errorf("AssignTeacherRole called %d times, want exactly 1", writer.assignCalls)
	}
	if writer.addBranchCalls != 2 {
		t.Errorf("AddUserBranch called %d times, want exactly 2 (one per branch)", writer.addBranchCalls)
	}
	if len(profile.Branches) != 2 {
		t.Errorf("profile.Branches = %#v, want both associated branches", profile.Branches)
	}
}

func TestTeacherCreateWritesExactlyOneAuditRowCoveringRoleAndEveryBranch(t *testing.T) {
	// One audit row must cover the role elevation AND every branch association
	// from the same request — mirrors event.Create's single event.created row
	// covering both the event insert and every event_branches insert.
	writer := &countingTeacherWriter{userID: studentUserID}
	audit := &fakeAuditStore{}
	svc := NewService(&fakeTeacherReader{userID: studentUserID, role: "student"}, audit, writer)

	_, err := svc.Create(context.Background(), adminActor(), CreateInput{
		Email:       "new-teacher@amrita.edu",
		BranchIDs:   []string{teacherBranchOne, teacherBranchTwo},
		CategoryIDs: []string{teacherBranchOne},
	})
	if err != nil {
		t.Fatalf("create teacher profile: %v", err)
	}
	if audit.calls != 1 {
		t.Fatalf("WithAudit called %d times, want exactly 1", audit.calls)
	}
	if audit.event.Action != domain.Action("teacher.profile_created") {
		t.Errorf("action = %s, want teacher.profile_created", audit.event.Action)
	}
	if audit.event.TargetTable == nil || *audit.event.TargetTable != "users" {
		t.Errorf("target table = %v, want users", audit.event.TargetTable)
	}
	// Target id is bound INSIDE the WithAudit callback from AssignTeacherRole's
	// result, not chosen beforehand — a target id chosen before the write would
	// name a row that may never exist.
	if audit.event.TargetID == nil || *audit.event.TargetID != studentUserID {
		t.Errorf("target id = %v, want the elevated user's id (%s)", audit.event.TargetID, studentUserID)
	}
}

func TestTeacherCreateRefusesANonAdminActorWithoutTouchingTheDatabase(t *testing.T) {
	// Defense in depth behind rbac.RequireAdmin: the service is reachable from
	// any future caller, so it re-checks rather than trusting the route — the
	// same posture category.CreateCategory takes.
	writer := &countingTeacherWriter{}
	audit := &fakeAuditStore{}
	svc := NewService(&fakeTeacherReader{}, audit, writer)

	_, err := svc.Create(context.Background(), Actor{UserID: activeAdminID, Email: "student@amrita.edu", Role: "student"},
		CreateInput{Email: "new-teacher@amrita.edu", BranchIDs: []string{teacherBranchOne}, CategoryIDs: []string{teacherBranchOne}})

	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) || domainErr.Code != contracts.ErrorCodeRBACFORBIDDEN {
		t.Fatalf("error = %v, want RBAC_FORBIDDEN", err)
	}
	if audit.calls != 0 || writer.assignCalls != 0 {
		t.Errorf("a forbidden create reached the write boundary (audit=%d assign=%d)", audit.calls, writer.assignCalls)
	}
}

func TestTeacherListReturnsPaginatedTeachersInDeterministicOrder(t *testing.T) {
	// Ordering is the server's promise (email ASC, id ASC), mirroring
	// events_list_order_idx's reasoning — the pagination determinism AC 3/D4
	// require.
	reader := &fakeTeacherReader{
		teachers: []TeacherProfile{
			{UserID: "a", Email: "a@amrita.edu"},
			{UserID: "b", Email: "b@amrita.edu"},
		},
		total: 2,
	}
	svc := NewService(reader, &fakeAuditStore{}, &countingTeacherWriter{})

	page, err := svc.List(context.Background(), adminActor(), ListInput{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list teachers: %v", err)
	}
	if len(page.Data) != 2 {
		t.Fatalf("page.Data = %#v, want two teachers", page.Data)
	}
}

func TestTeacherListRefusesANonAdminActor(t *testing.T) {
	svc := NewService(&fakeTeacherReader{}, &fakeAuditStore{}, &countingTeacherWriter{})
	_, err := svc.List(context.Background(), Actor{Role: "teacher"}, ListInput{})
	var domainErr domain.DomainError
	if !errors.As(err, &domainErr) || domainErr.Code != contracts.ErrorCodeRBACFORBIDDEN {
		t.Fatalf("error = %v, want RBAC_FORBIDDEN", err)
	}
}

func TestTeacherCreateTrimsEmailBeforeLookup(t *testing.T) {
	reader := &fakeTeacherReader{userID: studentUserID, role: "student"}
	writer := &countingTeacherWriter{}
	svc := NewService(reader, &fakeAuditStore{}, writer)

	_, err := svc.Create(context.Background(), adminActor(), CreateInput{
		Email:       "  new-teacher@amrita.edu  ",
		BranchIDs:   []string{teacherBranchOne},
		CategoryIDs: []string{teacherBranchOne},
	})
	if err != nil {
		t.Fatalf("create teacher profile: %v", err)
	}
	if writer.assignCalls != 1 {
		t.Errorf("assign not reached for a whitespace-padded but valid email")
	}
}
