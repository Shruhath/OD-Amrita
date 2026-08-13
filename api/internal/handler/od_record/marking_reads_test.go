// Handler tests for the coordinator marking reads.
//
// The one ordering rule these exist to protect: AUTHORIZE BEFORE VALIDATE.
// Story 0.3's review fixed exactly this defect in the card-token handlers. If a
// handler validates query parameters before it authorizes the principal, an
// unauthorized caller can tell a well-formed request from a malformed one by
// the difference between 400 and 403 — a free oracle over the parameter space.
// Every new endpoint in this story must answer 403 to an unauthorized caller
// even when the request is otherwise garbage.
package odrecord

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

	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
)

// 0.6-HTTP-001a — a non-live-marker is refused before any parameter is judged.
func TestMarkingReadHandlers_AuthorizeBeforeValidating(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name  string
		route string
		query string
	}{
		{"mark status with missing params", "/mark/status", ""},
		{"mark status with malformed uuids", "/mark/status", "?event_id=not-a-uuid&category_id=also-not"},
		{"search with a too-short query", "/students/search", "?q=a"},
		{"search with no query at all", "/students/search", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, svc := newMarkingReadRouter(t, actorRole("student"))

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1"+tc.route+tc.query, nil))

			if recorder.Code != http.StatusForbidden {
				t.Errorf("[P0] status = %d, want 403 — a student must be refused before the request shape is judged, "+
					"or the difference between 400 and 403 leaks which parameters are well-formed",
					recorder.Code)
			}
			if svc.calls != 0 {
				t.Errorf("[P0] the service was called %d times for an unauthorized request", svc.calls)
			}
			assertErrorEnvelope(t, recorder, contracts.ErrorCodeRBACFORBIDDEN)
		})
	}
}

func TestMarkingReadHandlers_AdminCanUseLiveMarkingReads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router, svc := newMarkingReadRouter(t, actorRole("admin"))

	for _, target := range []string{
		"/api/v1/events",
		"/api/v1/events/11111111-1111-4111-8111-111111111111/categories",
		"/api/v1/mark/status?event_id=11111111-1111-4111-8111-111111111111&category_id=33333333-3333-4333-8333-333333333333",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))

		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200 (body %s)", target, recorder.Code, recorder.Body.String())
		}
	}
	if svc.calls != 3 {
		t.Fatalf("service calls = %d, want 3", svc.calls)
	}
}

// 0.6-HTTP-001b — a coordinator sending a bad request gets a TYPED validation
// error through the centralized middleware, not a bare gin 400.
func TestMarkingReadHandlers_ReturnTypedValidationDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name      string
		target    string
		wantField string
	}{
		{"mark status without event_id", "/api/v1/mark/status?category_id=33333333-3333-4333-8333-333333333333", "event_id"},
		{"mark status without category_id", "/api/v1/mark/status?event_id=11111111-1111-4111-8111-111111111111", "category_id"},
		{"mark status with a malformed uuid", "/api/v1/mark/status?event_id=nope&category_id=33333333-3333-4333-8333-333333333333", "event_id"},
		{"search below the minimum length", "/api/v1/students/search?q=ab", "q"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, _ := newMarkingReadRouter(t, actorRole("coordinator"))

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.target, nil))

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("[P0] status = %d, want 400", recorder.Code)
			}

			var body contracts.ValidationError
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("[P0] response is not a generated ValidationError: %v (body %s)", err, recorder.Body.String())
			}
			if len(body.Details) == 0 {
				t.Fatal("[P0] validation error carries no details — the client cannot show the coordinator what to fix")
			}
			found := false
			for _, detail := range body.Details {
				if detail.Field == tc.wantField {
					found = true
				}
			}
			if !found {
				t.Errorf("[P0] no detail names field %q; got %+v", tc.wantField, body.Details)
			}
		})
	}
}

// 0.6-HTTP-001c — the search response never carries a token or an email, even
// when the service layer hands the handler more than it should. This is the
// last line of the AR20/NFR16 defence and it is cheap to keep.
func TestSearchHandler_ResponseCarriesNoTokenOrEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router, _ := newMarkingReadRouter(t, actorRole("coordinator"))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/api/v1/students/search?q=CB.SC.U4CSE24", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	var raw map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	body := recorder.Body.String()
	for _, banned := range []string{"token", "email", "fingerprint", "token_sha256"} {
		// Keys, via the decoded map — a nested `email` at any depth is caught.
		if containsKeyDeep(raw, banned) {
			t.Errorf("[P0] search response contains a %q key — students has no email column and tokens are fingerprint-only", banned)
		}
		// And the raw wire text, which catches the same term appearing as a VALUE
		// (an email leaked into display_name, a token echoed into a message). The
		// earlier version assigned `body` and then discarded it with `_ = body`,
		// so this half was never actually checked.
		if strings.Contains(strings.ToLower(body), banned) {
			t.Errorf("[P0] search response body contains %q as text: %s", banned, body)
		}
	}
}

// ---------------------------------------------------------------------------
// Harness.
// ---------------------------------------------------------------------------

// countingMarkingService records whether a handler reached the service at all.
// An unauthorized request must be refused before it gets here, so `calls` is the
// assertion that proves authorize-before-validate rather than merely implying it.
type countingMarkingService struct {
	calls int
}

func (s *countingMarkingService) VisibleEvents(context.Context, marking.Actor) ([]marking.Event, error) {
	s.calls++
	return []marking.Event{{
		EventID:   "11111111-1111-4111-8111-111111111111",
		Name:      "Marking Fixture",
		EventDate: time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC),
		Status:    "active",
		CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}}, nil
}

func (s *countingMarkingService) Categories(context.Context, marking.Actor, string) ([]marking.Category, error) {
	s.calls++
	return []marking.Category{{
		CategoryID: "33333333-3333-4333-8333-333333333333",
		EventID:    "11111111-1111-4111-8111-111111111111",
		Name:       "Procession",
		CreatedAt:  time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}}, nil
}

func (s *countingMarkingService) SearchStudents(context.Context, marking.Actor, string, int) ([]marking.Student, error) {
	s.calls++
	return []marking.Student{{
		StudentID:   "55555555-5555-4555-8555-555555555555",
		RollNo:      "CB.SC.U4CSE24001",
		DisplayName: "Ananya Rao",
		Branch:      "CSE",
		Section:     "A",
		Batch:       "2024",
	}}, nil
}

func (s *countingMarkingService) MarkStatus(context.Context, marking.Actor, string, string) (marking.Status, error) {
	s.calls++
	return marking.Status{
		ServerTime: time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC),
		Counters:   marking.Counters{Mine: 1, CategoryTotal: 2},
	}, nil
}

// newMarkingReadRouter composes the chain the way run() does for the
// authenticated group: the centralized error middleware, then a principal of the
// requested role, then the marking read routes.
func newMarkingReadRouter(t *testing.T, role string) (*gin.Engine, *countingMarkingService) {
	t.Helper()
	svc := &countingMarkingService{}

	router := gin.New()
	router.Use(errormw.Handler())
	router.Use(func(c *gin.Context) {
		c.Set("rbac.principal", rbac.Principal{
			UserID: uuid.NewString(),
			Email:  role + "@amrita.edu",
			Role:   role,
		})
		c.Next()
	})
	RegisterMarkingReadRoutes(router.Group("/api/v1"), svc)
	return router, svc
}

func actorRole(role string) string { return role }

func assertErrorEnvelope(t *testing.T, recorder *httptest.ResponseRecorder, want contracts.ErrorCode) {
	t.Helper()
	var body contracts.Error
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("[P0] response is not a generated Error envelope: %v (body %s)", err, recorder.Body.String())
	}
	if body.Code != want {
		t.Errorf("[P0] error code = %q, want %q", body.Code, want)
	}
}

// containsKeyDeep reports whether `key` appears anywhere in a decoded JSON tree.
func containsKeyDeep(node any, key string) bool {
	switch typed := node.(type) {
	case map[string]any:
		for k, v := range typed {
			if k == key || containsKeyDeep(v, key) {
				return true
			}
		}
	case []any:
		for _, v := range typed {
			if containsKeyDeep(v, key) {
				return true
			}
		}
	}
	return false
}
