package event

// category_handler_test.go — Story 2.2 coverage for AC 1, 2, 4 and 5 at the
// transport boundary. UNIT test: no build tag, no database.
//
// Invoked by: api-ci -> build job (`go test ./...`).
//
// categoryWireExpectations is the specification: the exact status code and
// envelope every outcome owes the caller, as data. The handler's whole job is to
// be a faithful translator, so the translation table is what the round trips
// range over rather than restating case by case.
//
// Boundary note: this file asserts transport only — status codes, wire bodies,
// body limits and malformed input. Business rules (rejection ORDER, the 23505
// mapping, audit contents) belong to service/event/category_test.go and are
// deliberately not duplicated here.

import (
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
	eventsvc "github.com/Shruhath/OD-Amrita/api/internal/service/event"
)

const (
	activeEventID = "20000000-0000-4000-8000-000000000001"
	otherEventID  = "20000000-0000-4000-8000-000000000004"
)

// fakeCategoryService embeds the Service interface so it satisfies it without
// restating the event methods. Those stay nil on purpose: if a category handler
// ever calls one, the test panics rather than passing quietly.
type fakeCategoryService struct {
	Service
	failWith    string
	categories  []eventsvc.Category
	createCalls int
	lastEventID string
	lastName    string
}

func (f *fakeCategoryService) ListCategories(_ context.Context, eventID string) ([]eventsvc.Category, error) {
	f.lastEventID = eventID
	if f.failWith != "" {
		return nil, categoryFailure(f.failWith)
	}
	return f.categories, nil
}

func (f *fakeCategoryService) CreateCategory(_ context.Context, _ eventsvc.Actor, input eventsvc.CreateCategoryInput) (eventsvc.Category, error) {
	f.createCalls++
	f.lastEventID = input.EventID
	f.lastName = input.Name
	if f.failWith != "" {
		return eventsvc.Category{}, categoryFailure(f.failWith)
	}
	return eventsvc.Category{
		ID:        uuid.NewString(),
		EventID:   input.EventID,
		Name:      input.Name,
		CreatedAt: time.Date(2026, 7, 21, 3, 0, 0, 0, time.UTC),
	}, nil
}

// categoryFailure turns a canonical code into the DomainError the real service
// would return, including the typed detail VALIDATION_FAILED always carries.
func categoryFailure(code string) error {
	err := domain.DomainError{Code: contracts.ErrorCode(code), Message: "Category request rejected."}
	if code == string(contracts.ErrorCodeVALIDATIONFAILED) {
		err.Details = []contracts.ValidationErrorDetail{
			{Field: "name", Code: "duplicate", Message: "A category with this name already exists."},
		}
	}
	return err
}

// performCategoryRequest composes the same errormw + identity + RequireAdmin
// chain the event handler tests use, then serves one request against the two
// category routes.
func performCategoryRequest(t *testing.T, svc Service, method, path, body string) *httptest.ResponseRecorder {
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
	router.GET("/api/v1/admin/events/:event_id/categories", ListCategoriesHandler(svc))
	router.POST("/api/v1/admin/events/:event_id/categories", CreateCategoryHandler(svc))
	return request(router, method, path, body)
}

// categoryWireExpectation is one service outcome and the response the caller
// must see. Every non-2xx flows through errormw as the centralized generated
// {code, message, details} envelope — a handler that writes its own error body
// would pass a naive smoke test and break every typed consumer.
type categoryWireExpectation struct {
	name        string
	serviceCode string // canonical code the fake service returns, empty for success
	wantStatus  int
	wantCode    string // expected envelope code, empty when the response is a success body
	wantDetails bool   // whether details[] must be present and non-empty
}

var categoryWireExpectations = []categoryWireExpectation{
	{name: "create succeeds", wantStatus: 201},
	{name: "duplicate name", serviceCode: "VALIDATION_FAILED", wantStatus: 400, wantCode: "VALIDATION_FAILED", wantDetails: true},
	{name: "unauthenticated", serviceCode: "AUTH_TOKEN_INVALID", wantStatus: 401, wantCode: "AUTH_TOKEN_INVALID"},
	{name: "non-admin", serviceCode: "RBAC_FORBIDDEN", wantStatus: 403, wantCode: "RBAC_FORBIDDEN"},
	{name: "unknown event", serviceCode: "EVENT_NOT_FOUND", wantStatus: 404, wantCode: "EVENT_NOT_FOUND"},
	{name: "archived event", serviceCode: "EVENT_ARCHIVED", wantStatus: 409, wantCode: "EVENT_ARCHIVED"},
	{name: "unexpected failure", serviceCode: "INTERNAL", wantStatus: 500, wantCode: "INTERNAL"},
}

// TestCategoryWireExpectationsCoverEveryDocumentedResponse runs today. It keeps
// the table honest against the contract scaffold in test/contract, which asserts
// the same status family from the other direction.
func TestCategoryWireExpectationsCoverEveryDocumentedResponse(t *testing.T) {
	byStatus := map[int]string{}
	for _, tc := range categoryWireExpectations {
		if existing, clash := byStatus[tc.wantStatus]; clash {
			t.Errorf("cases %q and %q both claim status %d", existing, tc.name, tc.wantStatus)
		}
		byStatus[tc.wantStatus] = tc.name

		if tc.wantStatus >= 400 && tc.wantCode == "" {
			t.Errorf("%s: every non-2xx must name its envelope code", tc.name)
		}
		if tc.wantStatus < 400 && tc.wantCode != "" {
			t.Errorf("%s: a success response must not carry an error code", tc.name)
		}
		if tc.wantDetails && tc.wantCode != "VALIDATION_FAILED" {
			t.Errorf("%s: only VALIDATION_FAILED carries details[]", tc.name)
		}
	}

	// POST is documented for 201/400/401/403/404/409/500 in the contract. A gap
	// here means a response the handler can emit that nothing pins.
	for _, status := range []int{201, 400, 401, 403, 404, 409, 500} {
		if _, ok := byStatus[status]; !ok {
			t.Errorf("no expectation covers status %d", status)
		}
	}
}

func TestCreateCategoryHandlerRendersEveryOutcomeThroughTheErrorEnvelope(t *testing.T) {
	for _, tc := range categoryWireExpectations {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeCategoryService{failWith: tc.serviceCode}
			recorder := performCategoryRequest(t, svc, http.MethodPost,
				"/api/v1/admin/events/"+activeEventID+"/categories",
				`{"name":"Procession"}`)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d body = %s, want %d", recorder.Code, recorder.Body.String(), tc.wantStatus)
			}

			if tc.wantStatus == http.StatusCreated {
				// The success body is the GENERATED type, not a hand-written
				// DTO: that is what keeps the wire and the contract in step.
				var created contracts.Category
				if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
					t.Fatalf("decode created category: %v", err)
				}
				if created.Name != "Procession" || created.EventId.String() != activeEventID {
					t.Errorf("created = %#v, want the persisted category scoped to its event", created)
				}
				if created.Id == uuid.Nil || created.CreatedAt.IsZero() {
					t.Errorf("created = %#v, want a server-assigned id and timestamp", created)
				}
				return
			}

			// VALIDATION_FAILED carries a typed details ARRAY, so it decodes as
			// the generated ValidationError; every other code decodes as the
			// generic Error, whose details are a freeform object. Using the
			// wrong one is itself a wire-shape error, which is why each branch
			// decodes into the type the contract actually promises.
			if tc.wantDetails {
				var validation contracts.ValidationError
				if err := json.Unmarshal(recorder.Body.Bytes(), &validation); err != nil {
					t.Fatalf("decode validation error: %v", err)
				}
				if string(validation.Code) != tc.wantCode {
					t.Errorf("code = %s, want %s", validation.Code, tc.wantCode)
				}
				if len(validation.Details) == 0 {
					t.Errorf("body = %s, want typed details", recorder.Body.String())
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
}

func TestCreateCategoryHandlerRejectsMalformedAndOversizedBodiesBeforeTheService(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		wantField string
		wantCode  string
	}{
		{name: "malformed json", body: `{"name":`, wantField: "$", wantCode: "invalid_json"},
		{name: "missing name", body: `{}`, wantField: "name", wantCode: "required"},
		{name: "name is not a string", body: `{"name":42}`, wantField: "name", wantCode: "invalid_format"},
		{name: "oversized body", body: `{"name":"` + strings.Repeat("a", 1<<20) + `"}`, wantField: "$", wantCode: "maximum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeCategoryService{}
			recorder := performCategoryRequest(t, svc, http.MethodPost,
				"/api/v1/admin/events/"+activeEventID+"/categories", tc.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", recorder.Code, recorder.Body.String())
			}

			// Transport validation stays FIELD-ADDRESSABLE. Collapsing these
			// into an undifferentiated JSON error is what bindCreateRequest
			// was written to avoid, and the portal needs the field to attach
			// the message to the right input.
			var validation contracts.ValidationError
			if err := json.Unmarshal(recorder.Body.Bytes(), &validation); err != nil {
				t.Fatalf("decode validation error: %v", err)
			}
			if len(validation.Details) != 1 ||
				validation.Details[0].Field != tc.wantField ||
				validation.Details[0].Code != tc.wantCode {
				t.Errorf("details = %#v, want field=%s code=%s", validation.Details, tc.wantField, tc.wantCode)
			}

			// The write layer must never see input the transport rejected.
			if svc.createCalls != 0 {
				t.Errorf("service reached %d times for invalid input", svc.createCalls)
			}
		})
	}
}

func TestCreateCategoryHandlerTakesTheEventIdFromThePathNotTheBody(t *testing.T) {
	// The path segment is the authorization-relevant identifier; a body field
	// would let a caller create a category under an event other than the one
	// the route was checked against.
	svc := &fakeCategoryService{}
	recorder := performCategoryRequest(t, svc, http.MethodPost,
		"/api/v1/admin/events/"+activeEventID+"/categories",
		`{"name":"Procession","event_id":"`+otherEventID+`"}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if svc.lastEventID != activeEventID {
		t.Errorf("service received event %s, want the path's event %s", svc.lastEventID, activeEventID)
	}
}

func TestListCategoriesHandlerReturnsTheUnpaginatedCollectionInServerOrder(t *testing.T) {
	svc := &fakeCategoryService{categories: []eventsvc.Category{
		{ID: uuid.NewString(), EventID: activeEventID, Name: "Procession"},
		{ID: uuid.NewString(), EventID: activeEventID, Name: "Stage Setup"},
	}}
	recorder := performCategoryRequest(t, svc, http.MethodGet,
		"/api/v1/admin/events/"+activeEventID+"/categories", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", recorder.Code, recorder.Body.String())
	}

	var collection contracts.CategoryCollection
	if err := json.Unmarshal(recorder.Body.Bytes(), &collection); err != nil {
		t.Fatalf("decode collection: %v", err)
	}
	if len(collection.Data) != 2 ||
		collection.Data[0].Name != "Procession" ||
		collection.Data[1].Name != "Stage Setup" {
		t.Errorf("data = %#v, want the service order preserved", collection.Data)
	}

	// An empty event returns an empty ARRAY, never null: a null here breaks
	// every consumer that maps over the collection without a guard.
	empty := performCategoryRequest(t, &fakeCategoryService{}, http.MethodGet,
		"/api/v1/admin/events/"+activeEventID+"/categories", "")
	if !strings.Contains(empty.Body.String(), `"data":[]`) {
		t.Errorf("empty body = %s, want an empty array", empty.Body.String())
	}
}

func TestListCategoriesHandlerRendersRejectionsThroughTheErrorEnvelope(t *testing.T) {
	// The read path has its own status family (no 409), and each one must still
	// arrive as the centralized envelope rather than a bespoke body.
	for _, tc := range []struct {
		code   string
		status int
	}{
		{code: "VALIDATION_FAILED", status: http.StatusBadRequest},
		{code: "AUTH_TOKEN_INVALID", status: http.StatusUnauthorized},
		{code: "RBAC_FORBIDDEN", status: http.StatusForbidden},
		{code: "EVENT_NOT_FOUND", status: http.StatusNotFound},
		{code: "INTERNAL", status: http.StatusInternalServerError},
	} {
		t.Run(tc.code, func(t *testing.T) {
			recorder := performCategoryRequest(t, &fakeCategoryService{failWith: tc.code},
				http.MethodGet, "/api/v1/admin/events/"+activeEventID+"/categories", "")

			if recorder.Code != tc.status {
				t.Fatalf("status = %d body = %s, want %d", recorder.Code, recorder.Body.String(), tc.status)
			}
			// Only the code is asserted here; the details shape is pinned by the
			// create-path table above.
			var envelope struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			if envelope.Code != tc.code {
				t.Errorf("code = %s, want %s", envelope.Code, tc.code)
			}
		})
	}
}

// TestBothCategoryRoutesAreMountedOnTheGuardedAdminGroup pins the fact the
// deny-matrix in middleware/rbac cannot: that the routes EXIST. A route the
// story forgets to mount returns 404, which reads as a frontend bug and is
// exactly how the portal integration job failed in Story 2.1's neighbourhood.
//
// It asserts against the SHARED registration function that both cmd/api and
// test/portal call, so production and the acceptance harness can no longer serve
// different route sets.
func TestBothCategoryRoutesAreMountedOnTheGuardedAdminGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterAdminRoutes(router.Group("/api/v1/admin"), &fakeCategoryService{})

	mounted := map[string]bool{}
	for _, route := range router.Routes() {
		mounted[route.Method+" "+route.Path] = true
	}
	for _, want := range []string{
		"GET /api/v1/admin/events/:event_id/categories",
		"POST /api/v1/admin/events/:event_id/categories",
	} {
		if !mounted[want] {
			t.Errorf("route %q is not mounted; the browser journey would see a 404", want)
		}
	}
	// Story 2.1's routes must survive the shared-registration refactor.
	for _, want := range []string{
		"GET /api/v1/admin/branches",
		"GET /api/v1/admin/events",
		"POST /api/v1/admin/events",
		"POST /api/v1/admin/events/:event_id/archive",
	} {
		if !mounted[want] {
			t.Errorf("Story 2.1 route %q was lost", want)
		}
	}
	// Scope is create + list only: no route may address a category by id.
	for path := range mounted {
		if strings.Contains(path, "/categories/") {
			t.Errorf("unexpected per-category route %q", path)
		}
	}
}
