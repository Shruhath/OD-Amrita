package event

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	eventsvc "github.com/Shruhath/OD-Amrita/api/internal/service/event"
)

type coordinatorAssignmentWireExpectation struct {
	name        string
	serviceCode string
	wantStatus  int
	wantCode    string
	wantDetails bool
}

var coordinatorAssignmentWireExpectations = []coordinatorAssignmentWireExpectation{
	{name: "create succeeds", wantStatus: 201},
	{name: "email validation fails", serviceCode: "VALIDATION_FAILED", wantStatus: 400, wantCode: "VALIDATION_FAILED", wantDetails: true},
	{name: "unauthenticated", serviceCode: "AUTH_TOKEN_INVALID", wantStatus: 401, wantCode: "AUTH_TOKEN_INVALID"},
	{name: "non-admin", serviceCode: "RBAC_FORBIDDEN", wantStatus: 403, wantCode: "RBAC_FORBIDDEN"},
	{name: "unknown user", serviceCode: "USER_NOT_FOUND", wantStatus: 404, wantCode: "USER_NOT_FOUND"},
	{name: "unknown event", serviceCode: "EVENT_NOT_FOUND", wantStatus: 404, wantCode: "EVENT_NOT_FOUND"},
	{name: "duplicate assignment", serviceCode: "COORDINATOR_ASSIGNMENT_EXISTS", wantStatus: 409, wantCode: "COORDINATOR_ASSIGNMENT_EXISTS"},
	{name: "archived event", serviceCode: "EVENT_ARCHIVED", wantStatus: 409, wantCode: "EVENT_ARCHIVED"},
	{name: "ineligible role", serviceCode: "ROLE_ALREADY_ASSIGNED", wantStatus: 409, wantCode: "ROLE_ALREADY_ASSIGNED"},
	{name: "unexpected failure", serviceCode: "INTERNAL", wantStatus: 500, wantCode: "INTERNAL"},
}

func TestCoordinatorAssignmentWireExpectationsCoverEveryDocumentedResponse(t *testing.T) {
	seenStatus := map[int]bool{}
	seen404 := map[string]bool{}
	seen409 := map[string]bool{}
	for _, row := range coordinatorAssignmentWireExpectations {
		seenStatus[row.wantStatus] = true
		if row.wantStatus == 404 {
			seen404[row.wantCode] = true
		}
		if row.wantStatus == 409 {
			seen409[row.wantCode] = true
		}
		if row.wantDetails != (row.wantCode == "VALIDATION_FAILED") {
			t.Fatalf("%s details=%v, want details only for VALIDATION_FAILED", row.name, row.wantDetails)
		}
	}
	for _, status := range []int{201, 400, 401, 403, 404, 409, 500} {
		if !seenStatus[status] {
			t.Fatalf("documented status %d is not covered", status)
		}
	}
	if len(seen404) != 2 || !seen404["USER_NOT_FOUND"] || !seen404["EVENT_NOT_FOUND"] {
		t.Fatalf("404 codes = %v, want USER_NOT_FOUND and EVENT_NOT_FOUND", seen404)
	}
	if len(seen409) != 3 ||
		!seen409["COORDINATOR_ASSIGNMENT_EXISTS"] ||
		!seen409["EVENT_ARCHIVED"] ||
		!seen409["ROLE_ALREADY_ASSIGNED"] {
		t.Fatalf("409 codes = %v, want duplicate, archived, and ineligible-role conflicts", seen409)
	}
}

func TestCreateCoordinatorAssignmentHandlerRendersEveryOutcomeThroughTheErrorEnvelope(t *testing.T) {
	eventID := uuid.NewString()
	assignment := eventsvc.CoordinatorAssignment{
		ID: uuid.NewString(), UserID: uuid.NewString(), Email: "candidate@amrita.edu",
		EventID: eventID, CreatedAt: time.Date(2026, 8, 3, 5, 0, 0, 0, time.UTC),
	}
	for _, row := range coordinatorAssignmentWireExpectations {
		t.Run(row.name, func(t *testing.T) {
			if row.serviceCode == "AUTH_TOKEN_INVALID" || row.serviceCode == "RBAC_FORBIDDEN" {
				return
			}
			svc := &fakeService{assignments: []eventsvc.CoordinatorAssignment{assignment}}
			if row.serviceCode == "INTERNAL" {
				svc.err = errors.New("database unavailable")
			} else if row.serviceCode != "" {
				svc.err = domain.DomainError{Code: contracts.ErrorCode(row.serviceCode), Message: row.name}
				if row.wantDetails {
					svc.err = domain.DomainError{
						Code: contracts.ErrorCode(row.serviceCode), Message: row.name,
						Details: []contracts.ValidationErrorDetail{{Field: "email", Code: "required", Message: "Email is required."}},
					}
				}
			}
			categoryID := uuid.NewString()
			recorder := request(eventRouter(svc), http.MethodPost, "/events/"+eventID+"/coordinators", `{"email":"candidate@amrita.edu","category_ids":["`+categoryID+`"]}`)
			if recorder.Code != row.wantStatus {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), row.wantStatus)
			}
			if row.wantStatus == http.StatusCreated {
				var body contracts.CoordinatorAssignment
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode success: %v", err)
				}
				if body.Email != "candidate@amrita.edu" || body.EventId.String() != eventID {
					t.Fatalf("body = %#v", body)
				}
				return
			}
			if row.wantDetails {
				var body contracts.ValidationError
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || string(body.Code) != row.wantCode {
					t.Fatalf("validation body=%s err=%v, want code %s", recorder.Body.String(), err, row.wantCode)
				}
				return
			}
			var body contracts.Error
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || string(body.Code) != row.wantCode {
				t.Fatalf("body=%s err=%v, want code %s", recorder.Body.String(), err, row.wantCode)
			}
		})
	}
}

func TestCreateCoordinatorAssignmentHandlerRejectsMalformedAndOversizedBodiesBeforeTheService(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{name: "malformed json", body: `{"email":`, code: "invalid_json"},
		{name: "missing email", body: `{}`, code: "required"},
		{name: "email is not string", body: `{"email":123,"category_ids":["` + uuid.NewString() + `"]}`, code: "invalid_format"},
		{name: "missing category ids", body: `{"email":"candidate@amrita.edu"}`, code: "required"},
		{name: "category id is not uuid", body: `{"email":"candidate@amrita.edu","category_ids":["bad"]}`, code: "invalid_format"},
		{name: "too large", body: `{"email":"` + strings.Repeat("x", int(maxCreateRequestBytes)) + `","category_ids":["` + uuid.NewString() + `"]}`, code: "maximum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{}
			recorder := request(eventRouter(svc), http.MethodPost, "/events/"+uuid.NewString()+"/coordinators", tc.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var body contracts.ValidationError
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode validation: %v", err)
			}
			if body.Message != "Coordinator assignment validation failed." {
				t.Fatalf("message = %q, want coordinator assignment validation message", body.Message)
			}
			if len(body.Details) != 1 || body.Details[0].Code != tc.code {
				t.Fatalf("details = %#v, want code %s", body.Details, tc.code)
			}
			if svc.coordinatorCreateCalls != 0 {
				t.Fatalf("service reached for invalid body")
			}
		})
	}
}

func TestCreateCoordinatorAssignmentHandlerTakesTheEventIdFromThePathNotTheBody(t *testing.T) {
	pathEventID := uuid.NewString()
	bodyEventID := uuid.NewString()
	assignment := eventsvc.CoordinatorAssignment{
		ID: uuid.NewString(), UserID: uuid.NewString(), Email: "candidate@amrita.edu",
		EventID: pathEventID, CreatedAt: time.Now(),
	}
	svc := &fakeService{assignments: []eventsvc.CoordinatorAssignment{assignment}}
	recorder := request(eventRouter(svc), http.MethodPost, "/events/"+pathEventID+"/coordinators",
		`{"email":"candidate@amrita.edu","event_id":"`+bodyEventID+`","category_ids":["`+uuid.NewString()+`"]}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if svc.coordinatorInput.EventID != pathEventID {
		t.Fatalf("service event id = %q, want path id %q", svc.coordinatorInput.EventID, pathEventID)
	}
}

func TestListCoordinatorAssignmentsHandlerReturnsTheUnpaginatedCollectionInServerOrder(t *testing.T) {
	eventID := uuid.NewString()
	svc := &fakeService{assignments: []eventsvc.CoordinatorAssignment{
		{ID: uuid.NewString(), UserID: uuid.NewString(), Email: "first@amrita.edu", EventID: eventID, CreatedAt: time.Now()},
		{ID: uuid.NewString(), UserID: uuid.NewString(), Email: "second@amrita.edu", EventID: eventID, CreatedAt: time.Now()},
	}}
	recorder := request(eventRouter(svc), http.MethodGet, "/events/"+eventID+"/coordinators", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body contracts.CoordinatorAssignmentCollection
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode collection: %v", err)
	}
	if len(body.Data) != 2 || body.Data[0].Email != "first@amrita.edu" || body.Data[1].Email != "second@amrita.edu" {
		t.Fatalf("data = %#v", body.Data)
	}
}

func TestRegisterAdminRoutesMountsCoordinatorAssignmentRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterAdminRoutes(router.Group("/admin"), &fakeService{})
	seen := map[string]bool{}
	for _, route := range router.Routes() {
		seen[route.Method+" "+route.Path] = true
	}
	for _, want := range []string{
		"GET /admin/events/:event_id/coordinators",
		"POST /admin/events/:event_id/coordinators",
	} {
		if !seen[want] {
			t.Fatalf("missing route %s in %#v", want, seen)
		}
	}
}

// Removal answers 204 with no body, and every failure goes through the shared
// error envelope with the status the contract documents.
func TestRemoveCoordinatorAssignmentHandlerRendersEveryDocumentedOutcome(t *testing.T) {
	eventID := uuid.NewString()
	userID := uuid.NewString()
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "removed", wantStatus: http.StatusNoContent},
		{name: "malformed id", err: domain.DomainError{Code: contracts.ErrorCodeVALIDATIONFAILED, Message: "bad", Details: []contracts.ValidationErrorDetail{{Field: "user_id", Code: "invalid_format", Message: "User id must be a UUID."}}}, wantStatus: 400, wantCode: "VALIDATION_FAILED"},
		{name: "non-admin", err: domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "no"}, wantStatus: 403, wantCode: "RBAC_FORBIDDEN"},
		{name: "unknown event", err: domain.DomainError{Code: contracts.ErrorCodeEVENTNOTFOUND, Message: "no"}, wantStatus: 404, wantCode: "EVENT_NOT_FOUND"},
		{name: "not assigned", err: domain.DomainError{Code: contracts.ErrorCodeUSERNOTFOUND, Message: "no"}, wantStatus: 404, wantCode: "USER_NOT_FOUND"},
		{name: "archived event", err: domain.DomainError{Code: contracts.ErrorCodeEVENTARCHIVED, Message: "no"}, wantStatus: 409, wantCode: "EVENT_ARCHIVED"},
		{name: "unexpected failure", err: errors.New("database unavailable"), wantStatus: 500, wantCode: "INTERNAL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{err: tc.err}
			recorder := request(eventRouter(svc), http.MethodDelete, "/events/"+eventID+"/coordinators/"+userID, "")
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), tc.wantStatus)
			}
			if svc.coordinatorRemoveCalls != 1 || svc.coordinatorRemoveInput.EventID != eventID || svc.coordinatorRemoveInput.UserID != userID {
				t.Fatalf("service calls=%d input=%#v", svc.coordinatorRemoveCalls, svc.coordinatorRemoveInput)
			}
			if tc.wantStatus == http.StatusNoContent {
				if recorder.Body.Len() != 0 {
					t.Fatalf("204 must carry no body, got %q", recorder.Body.String())
				}
				return
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != tc.wantCode {
				t.Fatalf("body=%s err=%v, want code %s", recorder.Body.String(), err, tc.wantCode)
			}
		})
	}
}
