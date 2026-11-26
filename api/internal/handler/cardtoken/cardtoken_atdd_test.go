package cardtoken

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	cardtokensvc "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
)

func TestSealHandlerRendersGeneratedPayloadOnlyShape(t *testing.T) {
	studentID := uuid.NewString()
	svc := &fakeService{seal: cardtokensvc.SealResult{
		Student: cardtokensvc.ResolvedStudent{
			StudentID: studentID, RollNo: "CB.SC.U4CSE99001", DisplayName: "Ananya Rao", Branch: "CSE", Section: "A", Batch: "2024",
		},
		Version: "v1",
		Token:   "QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI",
		Payload: cardtokensvc.CardPayload{
			Version: "v1",
			Token:   "QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI",
		},
	}}

	recorder := request(cardTokenRouter(svc, "admin"), http.MethodPost, "/students/"+studentID+"/card-seal", nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body contracts.CardSeal
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode CardSeal: %v", err)
	}
	if body.Version != "v1" || body.Token == "" {
		t.Fatalf("body = %+v, want version and token", body)
	}
	encoded, err := json.Marshal(body.Payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	for _, forbidden := range []string{"roll_no", "display_name", "student_id", "branch", "section", "batch", "email", "key"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("card payload leaked %s: %s", forbidden, encoded)
		}
	}
}

func TestResolveHandlerMapsKnownTokenAndUniformUnknownFailures(t *testing.T) {
	studentID := uuid.NewString()
	svc := &fakeService{resolved: cardtokensvc.ResolvedStudent{
		StudentID: studentID,
		RollNo:    "CB.SC.U4CSE99002", DisplayName: "Madhav Rao", Branch: "CSE", Section: "B", Batch: "2024",
	}}

	ok := request(cardTokenRouter(svc, "admin"), http.MethodPost, "/card-tokens/resolve", bytes.NewBufferString(`{"version":"v1","token":"QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI"}`))
	if ok.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", ok.Code, ok.Body.String())
	}
	var body contracts.ResolvedCardStudent
	if err := json.Unmarshal(ok.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ResolvedCardStudent: %v", err)
	}
	if body.StudentId.String() != studentID || body.RollNo != "CB.SC.U4CSE99002" {
		t.Fatalf("resolved body = %+v", body)
	}

	for _, requestBody := range []string{
		`{"version":"v1","token":"!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"}`,
		`{"version":"disabled","token":"QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI"}`,
		`{"version":"v1","token":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`,
	} {
		svc.err = domain.DomainError{Code: contracts.ErrorCodeCARDNOTRECOGNIZED, Message: "Card not recognized."}
		recorder := request(cardTokenRouter(svc, "admin"), http.MethodPost, "/card-tokens/resolve", bytes.NewBufferString(requestBody))
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status=%d body=%s, want 422", recorder.Code, recorder.Body.String())
		}
		var errBody contracts.Error
		if err := json.Unmarshal(recorder.Body.Bytes(), &errBody); err != nil || errBody.Code != contracts.ErrorCodeCARDNOTRECOGNIZED {
			t.Fatalf("body=%s err=%v, want CARD_NOT_RECOGNIZED", recorder.Body.String(), err)
		}
	}
}

func TestCardTokenAdminRoutesRejectNonAdminBeforeService(t *testing.T) {
	svc := &fakeService{err: errors.New("service must not be called")}
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/students/" + uuid.NewString() + "/card-seal", ""},
		{http.MethodPost, "/card-tokens/resolve", `{"version":"v1","token":"QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI"}`},
	} {
		recorder := request(cardTokenRouter(svc, "student"), tc.method, tc.path, bytes.NewBufferString(tc.body))
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s status=%d body=%s, want 403", tc.method, tc.path, recorder.Code, recorder.Body.String())
		}
	}
	if svc.calls != 0 {
		t.Fatalf("service was called %d times for non-admin requests", svc.calls)
	}
}

func TestResolveHandlerEnforcesGeneratedStringBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "blank version", body: `{"version":"","token":"QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI"}`},
		{name: "overlong version", body: `{"version":"version-name-over-16","token":"QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI"}`},
		{name: "short token", body: `{"version":"v1","token":"abc"}`},
		{name: "overlong token", body: `{"version":"v1","token":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{}
			recorder := request(cardTokenRouter(svc, "admin"), http.MethodPost, "/card-tokens/resolve", bytes.NewBufferString(tc.body))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
			}
			if svc.calls != 0 {
				t.Fatalf("service was called %d times for invalid request", svc.calls)
			}
		})
	}
}

type fakeService struct {
	seal     cardtokensvc.SealResult
	resolved cardtokensvc.ResolvedStudent
	section  []cardtokensvc.CardStatus
	err      error
	calls    int
}

func (f *fakeService) Seal(ctx context.Context, actor cardtokensvc.Actor, input cardtokensvc.SealInput) (cardtokensvc.SealResult, error) {
	f.calls++
	return f.seal, f.err
}

func (f *fakeService) Resolve(ctx context.Context, actor cardtokensvc.Actor, input cardtokensvc.ResolveInput) (cardtokensvc.ResolvedStudent, error) {
	f.calls++
	return f.resolved, f.err
}

func (f *fakeService) SectionStatus(context.Context, cardtokensvc.Actor, cardtokensvc.SectionFilter) ([]cardtokensvc.CardStatus, error) {
	f.calls++
	return f.section, f.err
}

func cardTokenRouter(svc Service, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errormw.Handler())
	router.Use(func(c *gin.Context) {
		c.Set("rbac.principal", rbac.Principal{UserID: uuid.NewString(), Email: role + "@amrita.edu", Role: role})
		c.Next()
	})
	admin := router.Group("")
	RegisterAdminRoutes(admin, svc)
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
