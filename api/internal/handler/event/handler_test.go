package event

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	eventsvc "github.com/Shruhath/OD-Amrita/api/internal/service/event"
)

type fakeService struct {
	branches               []eventsvc.Branch
	page                   eventsvc.Page
	event                  eventsvc.Event
	assignments            []eventsvc.CoordinatorAssignment
	err                    error
	createCalls            int
	getCalls               int
	coordinatorCreateCalls int
	coordinatorUpdateCalls int
	coordinatorInput       eventsvc.CreateCoordinatorAssignmentInput
	coordinatorUpdateInput eventsvc.UpdateCoordinatorCategoryAccessInput
	coordinatorRemoveCalls int
	coordinatorRemoveInput eventsvc.RemoveCoordinatorAssignmentInput
	creditLimitCalls       int
	creditLimitInput       eventsvc.CaptureCreditLimitInput
}

func (f *fakeService) Branches(context.Context) ([]eventsvc.Branch, error) { return f.branches, f.err }
func (f *fakeService) List(context.Context, eventsvc.ListInput) (eventsvc.Page, error) {
	return f.page, f.err
}
func (f *fakeService) Get(context.Context, eventsvc.Actor, string) (eventsvc.Event, error) {
	f.getCalls++
	return f.event, f.err
}
func (f *fakeService) Create(context.Context, eventsvc.Actor, eventsvc.CreateInput) (eventsvc.Event, error) {
	f.createCalls++
	return f.event, f.err
}
func (f *fakeService) ListCategories(context.Context, eventsvc.Actor, string) ([]eventsvc.Category, error) {
	return nil, f.err
}
func (f *fakeService) CreateCategory(context.Context, eventsvc.Actor, eventsvc.CreateCategoryInput) (eventsvc.Category, error) {
	return eventsvc.Category{}, f.err
}
func (f *fakeService) UpdateCategoryVisibility(context.Context, eventsvc.Actor, eventsvc.UpdateCategoryVisibilityInput) (eventsvc.Category, error) {
	return eventsvc.Category{}, f.err
}
func (f *fakeService) Archive(context.Context, eventsvc.Actor, string) (eventsvc.Event, error) {
	return f.event, f.err
}
func (f *fakeService) ListCoordinatorAssignments(context.Context, eventsvc.Actor, string) ([]eventsvc.CoordinatorAssignment, error) {
	return f.assignments, f.err
}
func (f *fakeService) CreateCoordinatorAssignment(_ context.Context, _ eventsvc.Actor, input eventsvc.CreateCoordinatorAssignmentInput) (eventsvc.CoordinatorAssignment, error) {
	f.coordinatorCreateCalls++
	f.coordinatorInput = input
	if len(f.assignments) > 0 {
		return f.assignments[0], f.err
	}
	return eventsvc.CoordinatorAssignment{}, f.err
}
func (f *fakeService) RemoveCoordinatorAssignment(_ context.Context, _ eventsvc.Actor, input eventsvc.RemoveCoordinatorAssignmentInput) error {
	f.coordinatorRemoveCalls++
	f.coordinatorRemoveInput = input
	return f.err
}

func (f *fakeService) UpdateCoordinatorCategoryAccess(_ context.Context, _ eventsvc.Actor, input eventsvc.UpdateCoordinatorCategoryAccessInput) (eventsvc.CoordinatorAssignment, error) {
	f.coordinatorUpdateCalls++
	f.coordinatorUpdateInput = input
	if len(f.assignments) > 0 {
		return f.assignments[0], f.err
	}
	return eventsvc.CoordinatorAssignment{}, f.err
}

func (f *fakeService) ListCaptureCredits(context.Context, eventsvc.Actor, string) (eventsvc.CaptureCreditSummary, error) {
	return eventsvc.CaptureCreditSummary{}, f.err
}

func (f *fakeService) UpdateCoordinatorCaptureCreditLimit(_ context.Context, _ eventsvc.Actor, input eventsvc.CaptureCreditLimitInput) (eventsvc.CaptureCreditUsage, error) {
	f.creditLimitCalls++
	f.creditLimitInput = input
	return eventsvc.CaptureCreditUsage{
		EventID: input.EventID,
		UserID:  input.UserID,
		Email:   "coord@amrita.edu",
		Limit:   input.Limit,
	}, f.err
}

func (f *fakeService) ResetCaptureCredits(context.Context, eventsvc.Actor, string, string) (eventsvc.CaptureCreditUsage, error) {
	return eventsvc.CaptureCreditUsage{}, f.err
}

func TestHandlersReturnGeneratedSuccessShapes(t *testing.T) {
	eventID := uuid.NewString()
	branchID := uuid.NewString()
	createdAt := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	svc := &fakeService{
		branches: []eventsvc.Branch{{ID: branchID, Code: "CSE", Name: "Computer Science"}},
		event: eventsvc.Event{
			ID: eventID, Name: "Innovation Day", EventDate: "2026-08-15", EndDate: "2026-08-15", Status: eventsvc.StatusActive,
			Branches: []eventsvc.Branch{{ID: branchID, Code: "CSE", Name: "Computer Science"}}, CreatedAt: createdAt,
		},
	}
	svc.page = eventsvc.Page{Data: []eventsvc.Event{svc.event}, Page: 1, PageSize: 20, Total: 1}
	router := eventRouter(svc)

	for _, tc := range []struct {
		method string
		path   string
		body   string
		status int
	}{
		{http.MethodGet, "/branches", "", http.StatusOK},
		{http.MethodGet, "/events", "", http.StatusOK},
		{http.MethodPost, "/events", `{"name":"Innovation Day","event_date":"2026-08-15","branch_ids":["` + branchID + `"]}`, http.StatusCreated},
		{http.MethodGet, "/events/" + eventID, "", http.StatusOK},
		{http.MethodPost, "/events/" + eventID + "/archive", "", http.StatusOK},
	} {
		recorder := request(router, tc.method, tc.path, tc.body)
		if recorder.Code != tc.status {
			t.Errorf("%s %s status=%d body=%s", tc.method, tc.path, recorder.Code, recorder.Body.String())
		}
	}
}

// The item read is the endpoint that replaced a client-side scan of the
// paginated collection, so its two failure shapes matter as much as its success:
// a malformed id must be a field error, and an unknown id must be the canonical
// 404 — never the "not on this page" silence the portal used to render.
func TestGetEventHandlerRendersTheGeneratedEventAndItsFailureShapes(t *testing.T) {
	eventID := uuid.NewString()
	branchID := uuid.NewString()
	svc := &fakeService{event: eventsvc.Event{
		ID: eventID, Name: "Innovation Day", EventDate: "2026-08-15", EndDate: "2026-08-15", Status: eventsvc.StatusActive,
		Branches:  []eventsvc.Branch{{ID: branchID, Code: "CSE", Name: "Computer Science"}},
		CreatedAt: time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC),
	}}

	recorder := request(eventRouter(svc), http.MethodGet, "/events/"+eventID, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", recorder.Code, recorder.Body.String())
	}
	var found contracts.Event
	if err := json.Unmarshal(recorder.Body.Bytes(), &found); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if found.Id.String() != eventID || found.Name != "Innovation Day" {
		t.Errorf("event = %#v, want the requested event", found)
	}
	if len(found.Branches) != 1 {
		t.Errorf("branches = %#v, want the event's branch associations", found.Branches)
	}

	notFound := &fakeService{err: domain.DomainError{
		Code: contracts.ErrorCodeEVENTNOTFOUND, Message: "Event not found.",
	}}
	recorder = request(eventRouter(notFound), http.MethodGet, "/events/"+uuid.NewString(), "")
	if recorder.Code != http.StatusNotFound {
		t.Errorf("unknown id status = %d body = %s, want 404", recorder.Code, recorder.Body.String())
	}
}

func TestCreateRejectsMalformedJSONBeforeService(t *testing.T) {
	svc := &fakeService{}
	recorder := request(eventRouter(svc), http.MethodPost, "/events", `{"name":`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body contracts.ValidationError
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(body.Details) != 1 || body.Details[0].Field != "$" || body.Details[0].Code != "invalid_json" {
		t.Fatalf("details=%#v", body.Details)
	}
	if svc.createCalls != 0 {
		t.Fatal("service called for malformed JSON")
	}
}

func TestCreateReturnsFieldSpecificGeneratedTypeValidationBeforeService(t *testing.T) {
	branchID := uuid.NewString()
	for _, tc := range []struct {
		name  string
		body  string
		field string
		code  string
	}{
		{name: "missing date", body: `{"name":"Event","branch_ids":["` + branchID + `"]}`, field: "event_date", code: "required"},
		{name: "invalid date", body: `{"name":"Event","event_date":"2026-02-31","branch_ids":["` + branchID + `"]}`, field: "event_date", code: "invalid_format"},
		{name: "invalid branch uuid", body: `{"name":"Event","event_date":"2026-08-15","branch_ids":["not-a-uuid"]}`, field: "branch_ids[0]", code: "invalid_format"},
		// end_date is OPTIONAL, so the generated struct makes it a POINTER and an
		// absent key is legal. A key that is PRESENT but unusable is the case T17
		// calls out: the generated unmarshaller would collapse each of these into
		// an undifferentiated JSON error, or — the bug 0.4's review actually
		// found — be dereferenced into a nil panic.
		//
		// These exercise bindCreateRequest's raw-shape guard, which is the real
		// client-facing boundary. The service-level suite in
		// service/event/date_range_test.go asserts strings such as "next friday"
		// that the handler can never produce, because it reformats a parsed
		// time.Time — so before these cases the guard itself had no coverage at
		// all anywhere in the repo.
		{name: "null end date", body: `{"name":"Event","event_date":"2026-08-15","end_date":null,"branch_ids":["` + branchID + `"]}`, field: "end_date", code: "invalid_format"},
		{name: "empty end date", body: `{"name":"Event","event_date":"2026-08-15","end_date":"","branch_ids":["` + branchID + `"]}`, field: "end_date", code: "invalid_format"},
		{name: "non-string end date", body: `{"name":"Event","event_date":"2026-08-15","end_date":20260815,"branch_ids":["` + branchID + `"]}`, field: "end_date", code: "invalid_format"},
		{name: "impossible end date", body: `{"name":"Event","event_date":"2026-08-15","end_date":"2026-02-31","branch_ids":["` + branchID + `"]}`, field: "end_date", code: "invalid_format"},
		{name: "day-first end date", body: `{"name":"Event","event_date":"2026-08-15","end_date":"15-08-2026","branch_ids":["` + branchID + `"]}`, field: "end_date", code: "invalid_format"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{}
			recorder := request(eventRouter(svc), http.MethodPost, "/events", tc.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var body contracts.ValidationError
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if len(body.Details) != 1 || body.Details[0].Field != tc.field || body.Details[0].Code != tc.code {
				t.Fatalf("details=%#v", body.Details)
			}
			if svc.createCalls != 0 {
				t.Fatal("service called for invalid generated field")
			}
		})
	}
}

func TestUpdateCoordinatorCaptureCreditLimitRejectsMissingAndMalformedLimitBeforeService(t *testing.T) {
	eventID := uuid.NewString()
	userID := uuid.NewString()
	for _, tc := range []struct {
		name  string
		body  string
		field string
		code  string
	}{
		{name: "missing", body: `{}`, field: "limit", code: "required"},
		{name: "null", body: `{"limit":null}`, field: "limit", code: "invalid_format"},
		{name: "fraction", body: `{"limit":1.5}`, field: "limit", code: "invalid_format"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{}
			recorder := request(eventRouter(svc), http.MethodPut, "/events/"+eventID+"/capture-credits/coordinators/"+userID+"/limit", tc.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var body contracts.ValidationError
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if len(body.Details) != 1 || body.Details[0].Field != tc.field || body.Details[0].Code != tc.code {
				t.Fatalf("details=%#v", body.Details)
			}
			if svc.creditLimitCalls != 0 {
				t.Fatal("service called for invalid limit")
			}
		})
	}
}

func TestUpdateCoordinatorCaptureCreditLimitPreservesExplicitZero(t *testing.T) {
	eventID := uuid.NewString()
	userID := uuid.NewString()
	svc := &fakeService{}
	recorder := request(eventRouter(svc), http.MethodPut, "/events/"+eventID+"/capture-credits/coordinators/"+userID+"/limit", `{"limit":0}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if svc.creditLimitCalls != 1 || svc.creditLimitInput.Limit != 0 || svc.creditLimitInput.UserID != userID {
		t.Fatalf("calls/input = %d/%#v, want explicit zero passed to service", svc.creditLimitCalls, svc.creditLimitInput)
	}
}

func TestCreateRejectsBodyLargerThanOneMiBBeforeService(t *testing.T) {
	svc := &fakeService{}
	body := `{"name":"` + strings.Repeat("x", int(maxCreateRequestBytes)) + `","event_date":"2026-08-15","branch_ids":[]}`
	recorder := request(eventRouter(svc), http.MethodPost, "/events", body)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response contracts.ValidationError
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(response.Details) != 1 || response.Details[0].Field != "$" || response.Details[0].Code != "maximum" {
		t.Fatalf("details=%#v", response.Details)
	}
	if svc.createCalls != 0 {
		t.Fatal("service called for oversized request")
	}
}

func TestListReturnsTypedValidationForInvalidQuery(t *testing.T) {
	for _, tc := range []struct {
		query string
		field string
		code  string
	}{
		{query: "?status=deleted", field: "status", code: "invalid_enum"},
		{query: "?page=0", field: "page", code: "minimum"},
		{query: "?page_size=101", field: "page_size", code: "maximum"},
	} {
		svc := &fakeService{err: domain.DomainError{
			Code:    contracts.ErrorCodeVALIDATIONFAILED,
			Message: "Event validation failed.",
			Details: []contracts.ValidationErrorDetail{{Field: tc.field, Code: tc.code, Message: "Invalid query value."}},
		}}
		recorder := request(eventRouter(svc), http.MethodGet, "/events"+tc.query, "")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("query=%s status=%d body=%s", tc.query, recorder.Code, recorder.Body.String())
		}
		var body contracts.ValidationError
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("query=%s decode error: %v", tc.query, err)
		}
		if len(body.Details) != 1 || body.Details[0].Field != tc.field || body.Details[0].Code != tc.code {
			t.Fatalf("query=%s details=%#v", tc.query, body.Details)
		}
	}
}

func TestHandlersRender404And500Centrally(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   contracts.ErrorCode
	}{
		{name: "not found", err: domain.DomainError{Code: contracts.ErrorCodeEVENTNOTFOUND, Message: "Event not found."}, status: 404, code: contracts.ErrorCodeEVENTNOTFOUND},
		{name: "internal", err: errors.New("database unavailable"), status: 500, code: contracts.ErrorCodeINTERNAL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := request(eventRouter(&fakeService{err: tc.err}), http.MethodPost, "/events/"+uuid.NewString()+"/archive", "")
			if recorder.Code != tc.status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var body contracts.Error
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != tc.code {
				t.Fatalf("body=%s err=%v", recorder.Body.String(), err)
			}
		})
	}
}

func eventRouter(svc Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errormw.Handler())
	router.Use(func(c *gin.Context) {
		mwauth.SetIdentity(c, svcauth.VerifiedIdentity{Subject: "admin-subject", Email: "admin@amrita.edu"})
		c.Next()
	})
	router.Use(rbac.RequireAdmin(staticPrincipalLoader{principal: rbac.Principal{UserID: uuid.NewString(), Email: "admin@amrita.edu", Role: "admin"}}))
	router.GET("/branches", ListBranchesHandler(svc))
	router.GET("/events", ListEventsHandler(svc))
	router.POST("/events", CreateEventHandler(svc))
	router.GET("/events/:event_id", GetEventHandler(svc))
	router.POST("/events/:event_id/archive", ArchiveEventHandler(svc))
	router.GET("/events/:event_id/coordinators", ListCoordinatorAssignmentsHandler(svc))
	router.POST("/events/:event_id/coordinators", CreateCoordinatorAssignmentHandler(svc))
	router.PUT("/events/:event_id/coordinators/:user_id/categories", UpdateCoordinatorCategoryAccessHandler(svc))
	router.DELETE("/events/:event_id/coordinators/:user_id", RemoveCoordinatorAssignmentHandler(svc))
	router.GET("/events/:event_id/capture-credits", ListCaptureCreditsHandler(svc))
	router.PUT("/events/:event_id/capture-credits/coordinators/:user_id/limit", UpdateCoordinatorCaptureCreditLimitHandler(svc))
	router.POST("/events/:event_id/capture-credits/coordinators/:user_id/reset", ResetCaptureCreditsHandler(svc))
	return router
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
