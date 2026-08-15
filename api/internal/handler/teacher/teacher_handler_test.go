package teacher

// teacher_handler_test.go — Story 2.3 coverage for AC 1, 2, 4 and 5 at the
// transport boundary. UNIT test: no build tag, no database.
//
// Invoked by: api-ci -> build job (`go test ./...`).
//
// Boundary note: this file asserts transport only — status codes, wire
// bodies, body limits and malformed input. Business rules (validation order,
// the AssignTeacherRole race mapping, audit contents) belong to
// service/teacher/teacher_test.go and are deliberately not duplicated here.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
	teachersvc "github.com/Shruhath/OD-Amrita/api/internal/service/teacher"
)

const teacherBranchOne = "10000000-0000-4000-8000-000000000001"
const teacherCategoryOne = "10000000-0000-4000-8000-000000000011"

// fakeTeacherService is the transport-layer double: it never validates, it
// only records what it was called with and returns whatever failWith/teachers
// dictates — real validation behavior belongs to service/teacher's own tests.
type fakeTeacherService struct {
	failWith       string
	teachers       []teachersvc.TeacherProfile
	createCalls    int
	updateCalls    int
	lastEmail      string
	lastBranch     []string
	lastCategories []string
}

func (f *fakeTeacherService) List(context.Context, teachersvc.Actor, teachersvc.ListInput) (teachersvc.Page, error) {
	if f.failWith != "" {
		return teachersvc.Page{}, teacherFailure(f.failWith)
	}
	return teachersvc.Page{Data: f.teachers, Page: 1, PageSize: 20, Total: int64(len(f.teachers))}, nil
}

func (f *fakeTeacherService) Create(_ context.Context, _ teachersvc.Actor, input teachersvc.CreateInput) (teachersvc.TeacherProfile, error) {
	f.createCalls++
	f.lastEmail = input.Email
	f.lastBranch = input.BranchIDs
	if f.failWith != "" {
		return teachersvc.TeacherProfile{}, teacherFailure(f.failWith)
	}
	return teachersvc.TeacherProfile{
		UserID:    uuid.NewString(),
		Email:     input.Email,
		Branches:  []teachersvc.Branch{{ID: teacherBranchOne, Code: "CSE", Name: "Computer Science"}},
		CreatedAt: time.Date(2026, 7, 23, 3, 0, 0, 0, time.UTC),
	}, nil
}

func (f *fakeTeacherService) UpdateCategoryAccess(_ context.Context, _ teachersvc.Actor, input teachersvc.UpdateCategoryAccessInput) (teachersvc.TeacherProfile, error) {
	f.updateCalls++
	f.lastCategories = input.CategoryIDs
	if f.failWith != "" {
		return teachersvc.TeacherProfile{}, teacherFailure(f.failWith)
	}
	return teachersvc.TeacherProfile{
		UserID:     input.UserID,
		Email:      "teacher@amrita.edu",
		Branches:   []teachersvc.Branch{{ID: teacherBranchOne, Code: "CSE", Name: "Computer Science"}},
		Categories: []teachersvc.Category{{ID: teacherCategoryOne, EventID: uuid.NewString(), Name: "Procession", CreatedAt: time.Now()}},
		CreatedAt:  time.Date(2026, 7, 23, 3, 0, 0, 0, time.UTC),
	}, nil
}

// teacherFailure turns a canonical code into the DomainError the real service
// would return, including the typed detail VALIDATION_FAILED always carries.
func teacherFailure(code string) error {
	err := domain.DomainError{Code: contracts.ErrorCode(code), Message: "Teacher profile request rejected."}
	if code == string(contracts.ErrorCodeVALIDATIONFAILED) {
		err.Details = []contracts.ValidationErrorDetail{
			{Field: "branch_ids", Code: "duplicate", Message: "Branch ids must be unique."},
		}
	}
	return err
}

func performTeacherRequest(t *testing.T, svc Service, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errormw.Handler())
	router.Use(func(c *gin.Context) {
		mwauth.SetIdentity(c, svcauth.VerifiedIdentity{Subject: "admin-subject", Email: "admin@amrita.edu"})
		c.Next()
	})
	router.Use(rbac.RequireAdmin(staticPrincipalLoader{principal: rbac.Principal{
		UserID: uuid.NewString(), Email: "admin@amrita.edu", Role: "admin",
	}}))
	RegisterAdminRoutes(router.Group("/api/v1/admin"), svc)
	return request(router, method, path, body)
}

type staticPrincipalLoader struct{ principal rbac.Principal }

func (s staticPrincipalLoader) Load(context.Context, string) (rbac.Principal, error) {
	return s.principal, nil
}

func request(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, req)
	return recorder
}

// teacherWireExpectation is one service outcome and the response the caller
// must see. Every non-2xx flows through errormw as the centralized generated
// {code, message, details} envelope — a handler that writes its own error
// body would pass a naive smoke test and break every typed consumer.
type teacherWireExpectation struct {
	name        string
	serviceCode string // canonical code the fake service returns, empty for success
	wantStatus  int
	wantCode    string // expected envelope code, empty when the response is a success body
	wantDetails bool   // whether details[] must be present and non-empty
}

var teacherWireExpectations = []teacherWireExpectation{
	{name: "create succeeds", wantStatus: 201},
	{name: "malformed request", serviceCode: "VALIDATION_FAILED", wantStatus: 400, wantCode: "VALIDATION_FAILED", wantDetails: true},
	{name: "unauthenticated", serviceCode: "AUTH_TOKEN_INVALID", wantStatus: 401, wantCode: "AUTH_TOKEN_INVALID"},
	{name: "non-admin", serviceCode: "RBAC_FORBIDDEN", wantStatus: 403, wantCode: "RBAC_FORBIDDEN"},
	{name: "unknown email", serviceCode: "USER_NOT_FOUND", wantStatus: 404, wantCode: "USER_NOT_FOUND"},
	{name: "already-elevated role", serviceCode: "ROLE_ALREADY_ASSIGNED", wantStatus: 409, wantCode: "ROLE_ALREADY_ASSIGNED"},
	{name: "unexpected failure", serviceCode: "INTERNAL", wantStatus: 500, wantCode: "INTERNAL"},
}

// TestTeacherWireExpectationsCoverEveryDocumentedResponse is the ACTIVE guard
// that keeps the table above exhaustive against the contract's documented POST
// response family (201/400/401/403/404/409/500 — see
// test/contract/teacher_contract_test.go's response-family assertion), the
// same shape Story 2.2's TestCategoryWireExpectationsCoverEveryDocumentedResponse
// established.
func TestTeacherWireExpectationsCoverEveryDocumentedResponse(t *testing.T) {
	seen := map[int]bool{}
	for _, tc := range teacherWireExpectations {
		seen[tc.wantStatus] = true
		if tc.wantStatus != 201 && tc.wantCode == "" {
			t.Errorf("[P1] %s: non-success case must document an envelope code", tc.name)
		}
		if tc.wantStatus == 400 && !tc.wantDetails {
			t.Errorf("[P1] %s: 400 must be VALIDATION_FAILED with typed details, per the contract", tc.name)
		}
		if tc.wantDetails && tc.wantCode != "VALIDATION_FAILED" {
			t.Errorf("[P1] %s: only VALIDATION_FAILED may carry details[], got code=%s", tc.name, tc.wantCode)
		}
	}
	for _, status := range []int{201, 400, 401, 403, 404, 409, 500} {
		if !seen[status] {
			t.Errorf("[P0] the expectation table never claims a documented %d response", status)
		}
	}
}

func TestCreateTeacherHandlerRendersEveryOutcomeThroughTheErrorEnvelope(t *testing.T) {
	observed := map[int]string{}
	for _, tc := range teacherWireExpectations {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeTeacherService{failWith: tc.serviceCode}
			recorder := performTeacherRequest(t, svc, http.MethodPost, "/api/v1/admin/teachers",
				`{"email":"new-teacher@amrita.edu","branch_ids":["`+teacherBranchOne+`"],"category_ids":["`+teacherCategoryOne+`"]}`)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d body = %s, want %d", recorder.Code, recorder.Body.String(), tc.wantStatus)
			}
			observed[recorder.Code] = tc.name

			if tc.wantStatus == http.StatusCreated {
				var created contracts.TeacherProfile
				if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
					t.Fatalf("decode created teacher profile: %v", err)
				}
				if created.Email != "new-teacher@amrita.edu" {
					t.Errorf("created = %#v, want the elevated user's email", created)
				}
				return
			}
			if tc.wantDetails {
				var validation contracts.ValidationError
				if err := json.Unmarshal(recorder.Body.Bytes(), &validation); err != nil {
					t.Fatalf("decode validation error: %v", err)
				}
				if string(validation.Code) != tc.wantCode || len(validation.Details) == 0 {
					t.Errorf("body = %s, want code=%s with typed details", recorder.Body.String(), tc.wantCode)
				}
				return
			}
			var envelope contracts.Error
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			if string(envelope.Code) != tc.wantCode {
				t.Errorf("code = %s, want %s", envelope.Code, tc.wantCode)
			}
		})
	}
	for _, status := range []int{201, 400, 401, 403, 404, 409, 500} {
		if _, ok := observed[status]; !ok {
			t.Errorf("[P0] the handler never produced a documented %d response", status)
		}
	}
}

func TestCreateTeacherHandlerRejectsMalformedAndOversizedBodiesBeforeTheService(t *testing.T) {
	for _, tc := range []struct{ name, body, wantField, wantCode string }{
		{name: "malformed json", body: `{"email":`, wantField: "$", wantCode: "invalid_json"},
		{name: "missing email", body: `{"branch_ids":["` + teacherBranchOne + `"],"category_ids":["` + teacherCategoryOne + `"]}`, wantField: "email", wantCode: "required"},
		{name: "email is not a string", body: `{"email":42,"branch_ids":["` + teacherBranchOne + `"],"category_ids":["` + teacherCategoryOne + `"]}`, wantField: "email", wantCode: "invalid_format"},
		{name: "missing branch_ids", body: `{"email":"new-teacher@amrita.edu","category_ids":["` + teacherCategoryOne + `"]}`, wantField: "branch_ids", wantCode: "required"},
		{name: "missing category_ids", body: `{"email":"new-teacher@amrita.edu","branch_ids":["` + teacherBranchOne + `"]}`, wantField: "category_ids", wantCode: "required"},
		{name: "oversized body", body: `{"email":"` + strings.Repeat("a", 1<<20) + `"}`, wantField: "$", wantCode: "maximum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeTeacherService{}
			recorder := performTeacherRequest(t, svc, http.MethodPost, "/api/v1/admin/teachers", tc.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", recorder.Code, recorder.Body.String())
			}
			var validation contracts.ValidationError
			if err := json.Unmarshal(recorder.Body.Bytes(), &validation); err != nil {
				t.Fatalf("decode validation error: %v", err)
			}
			if len(validation.Details) != 1 || validation.Details[0].Field != tc.wantField || validation.Details[0].Code != tc.wantCode {
				t.Errorf("details = %#v, want field=%s code=%s", validation.Details, tc.wantField, tc.wantCode)
			}
			if svc.createCalls != 0 {
				t.Errorf("service reached %d times for invalid input", svc.createCalls)
			}
		})
	}
}

func TestListTeachersHandlerReturnsThePaginatedCollection(t *testing.T) {
	svc := &fakeTeacherService{teachers: []teachersvc.TeacherProfile{
		{UserID: uuid.NewString(), Email: "a@amrita.edu", Branches: []teachersvc.Branch{}, CreatedAt: time.Now()},
	}}
	recorder := performTeacherRequest(t, svc, http.MethodGet, "/api/v1/admin/teachers?page=1&page_size=20", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", recorder.Code, recorder.Body.String())
	}
	var collection contracts.TeacherProfileCollection
	if err := json.Unmarshal(recorder.Body.Bytes(), &collection); err != nil {
		t.Fatalf("decode collection: %v", err)
	}
	if collection.Page != 1 || collection.PageSize != 20 {
		t.Errorf("collection = %#v, want page=1 page_size=20", collection)
	}
}

// TestBothTeacherRoutesAreMountedOnTheGuardedAdminGroup pins the fact the deny
// matrix in middleware/rbac cannot: that the routes EXIST. A route the story
// forgets to mount returns 404, which reads as a frontend bug.
func TestBothTeacherRoutesAreMountedOnTheGuardedAdminGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterAdminRoutes(router.Group("/api/v1/admin"), &fakeTeacherService{})
	mounted := map[string]bool{}
	for _, route := range router.Routes() {
		mounted[route.Method+" "+route.Path] = true
	}
	for _, want := range []string{
		"GET /api/v1/admin/teachers",
		"POST /api/v1/admin/teachers",
		"PUT /api/v1/admin/teachers/:user_id/categories",
	} {
		if !mounted[want] {
			t.Errorf("route %q is not mounted; the browser journey would see a 404", want)
		}
	}
}
