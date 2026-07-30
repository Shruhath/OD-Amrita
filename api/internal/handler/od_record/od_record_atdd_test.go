//go:build atdd

package odrecord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
)

func TestMarkHandlerCreatesGeneratedResultEnvelope(t *testing.T) {
	eventID := uuid.NewString()
	categoryID := uuid.NewString()
	studentID := uuid.NewString()
	svc := &fakeService{result: odsvc.MarkResult{
		RecordID:         uuid.NewString(),
		EventID:          eventID,
		CategoryID:       categoryID,
		Slot:             1,
		CaptureMethod:    odsvc.CaptureMethodNFC,
		ServerReceivedAt: time.Date(2026, 8, 3, 8, 25, 0, 0, time.UTC),
		Student:          odsvc.Student{StudentID: studentID, RollNo: "CB.SC.U4CSE24001", DisplayName: "Ananya Rao", Branch: "CSE", Section: "A", Batch: "2024"},
	}}
	body := `{"event_id":"` + eventID + `","category_id":"` + categoryID + `","capture_method":"nfc","card":{"version":"v1","token":"QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI"}}`

	recorder := request(markRouter(svc, rbac.Principal{UserID: uuid.NewString(), Email: "coordinator@amrita.edu", Role: "coordinator"}), http.MethodPost, "/mark", bytes.NewBufferString(body))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response contracts.MarkAttendanceResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode MarkAttendanceResult: %v", err)
	}
	if response.Student.DisplayName != "Ananya Rao" || response.Slot != 1 || response.CaptureMethod != contracts.CaptureMethodNfc {
		t.Fatalf("response = %+v", response)
	}
	if svc.calls != 1 || svc.lastActor.Role != "coordinator" || svc.lastInput.Card == nil {
		t.Fatalf("service call = %d actor=%+v input=%+v", svc.calls, svc.lastActor, svc.lastInput)
	}
}

func TestMarkHandlerValidatesExactlyOneIdentitySourceAndTypedErrors(t *testing.T) {
	eventID := uuid.NewString()
	categoryID := uuid.NewString()
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "missing identity", body: `{"event_id":"` + eventID + `","category_id":"` + categoryID + `","capture_method":"search"}`},
		{name: "null identity", body: `{"event_id":"` + eventID + `","category_id":"` + categoryID + `","capture_method":"search","student_id":null}`},
		{name: "both identity sources", body: `{"event_id":"` + eventID + `","category_id":"` + categoryID + `","capture_method":"search","student_id":"` + uuid.NewString() + `","card":{"version":"v1","token":"token"}}`},
		{name: "coordinator supplies slot", body: `{"event_id":"` + eventID + `","category_id":"` + categoryID + `","capture_method":"nfc","slot":1,"card":{"version":"v1","token":"token"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{}
			recorder := request(markRouter(svc, rbac.Principal{UserID: uuid.NewString(), Email: "coordinator@amrita.edu", Role: "coordinator"}), http.MethodPost, "/mark", bytes.NewBufferString(tc.body))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
			}
			var errBody contracts.ValidationError
			if err := json.Unmarshal(recorder.Body.Bytes(), &errBody); err != nil {
				t.Fatalf("decode validation error: %v body=%s", err, recorder.Body.String())
			}
			if errBody.Code != contracts.ValidationErrorCodeVALIDATIONFAILED || len(errBody.Details) == 0 {
				t.Fatalf("validation error = %+v, want details", errBody)
			}
			if svc.calls != 0 {
				t.Fatalf("service was called %d times for invalid request", svc.calls)
			}
		})
	}
}

func TestMarkHandlerRequiresAuthenticatedPrincipalBeforeBodyValidation(t *testing.T) {
	svc := &fakeService{err: errors.New("service must not be called")}
	recorder := request(markRouter(svc, rbac.Principal{}), http.MethodPost, "/mark", bytes.NewBufferString(`{}`))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want 403 before request validation details", recorder.Code, recorder.Body.String())
	}
	if svc.calls != 0 {
		t.Fatalf("service was called %d times without a principal", svc.calls)
	}
}

func TestMarkRouteRegistrationIsOutsideAdminPrefix(t *testing.T) {
	router := markRouter(&fakeService{}, rbac.Principal{UserID: uuid.NewString(), Email: "coordinator@amrita.edu", Role: "coordinator"})
	var foundMark, foundAdminMark bool
	for _, route := range router.Routes() {
		if route.Method == http.MethodPost && route.Path == "/mark" {
			foundMark = true
		}
		if route.Method == http.MethodPost && route.Path == "/admin/mark" {
			foundAdminMark = true
		}
	}
	if !foundMark {
		t.Fatal("[P0] RegisterRoutes must mount POST /mark on the authenticated API group")
	}
	if foundAdminMark {
		t.Fatal("[P0] POST /mark must not be mounted under /admin")
	}
}

type fakeService struct {
	result    odsvc.MarkResult
	err       error
	calls     int
	lastActor odsvc.Actor
	lastInput odsvc.MarkInput
}

func (f *fakeService) Mark(ctx context.Context, actor odsvc.Actor, input odsvc.MarkInput) (odsvc.MarkResult, error) {
	f.calls++
	f.lastActor = actor
	f.lastInput = input
	return f.result, f.err
}

func markRouter(svc Service, principal rbac.Principal) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errormw.Handler())
	router.Use(func(c *gin.Context) {
		if principal.UserID != "" {
			c.Set("rbac.principal", principal)
		}
		c.Next()
	})
	RegisterRoutes(router.Group(""), svc)
	return router
}

func request(router *gin.Engine, method, path string, body *bytes.Buffer) *httptest.ResponseRecorder {
	if body == nil {
		body = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}
