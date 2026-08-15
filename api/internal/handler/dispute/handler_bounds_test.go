// Edge bounding for the public dispute POST.
//
// This is the endpoint the whole internet can reach, and the generated
// PublicDisputeRequest carries VALUE fields with no binding tags — so an absent
// or JSON-null required field decodes to a zero value that looks legitimate:
// uuid.Nil parses cleanly, and openapi_types.Date renders as 0001-01-01. Both
// used to travel past validation into the database, where a missing event_id
// came back as EVENT_NOT_FOUND (404) and a missing od_date as an unmapped NOT
// NULL violation. AC 4 requires every one of these to be a typed
// VALIDATION_FAILED, refused before any query.
//
// Each case asserts BOTH the envelope and that the service was never called, so
// "bounded at the edge" is a claim about the query that did not happen, not just
// about the status code.
package dispute

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	svcdispute "github.com/Shruhath/OD-Amrita/api/internal/service/dispute"
	"github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
)

// countingPublicService fails the test if it is reached at all.
type countingPublicService struct{ submits int }

func (s *countingPublicService) Submit(context.Context, svcdispute.SubmitInput) (svcdispute.Receipt, error) {
	s.submits++
	return svcdispute.Receipt{}, nil
}

func (s *countingPublicService) PublicEvents(context.Context) (publicattendance.EventCollection, error) {
	return publicattendance.EventCollection{}, nil
}

func newBoundsRouter(svc PublicService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errormw.Handler())
	RegisterPublicRoutes(router.Group("/api/v1/public"), svc, func(*gin.Context) {})
	return router
}

func postBody(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/disputes", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// completeBody is a body that would be accepted; each case below removes or
// nulls exactly one field from it, so a failure is attributable to that field.
func completeBody(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	body := map[string]any{
		"student_id":               "95000000-0000-4000-8000-000000000101",
		"submitted_email":          "cb.sc.u4cse24101@cb.students.amrita.edu",
		"event_id":                 "93000000-0000-4000-8000-000000000001",
		"category_id":              "94000000-0000-4000-8000-000000000001",
		"od_date":                  "2026-08-03",
		"slot":                     3,
		"claimed_coordinator_name": "Priya Menon",
		"student_note":             "I was present in the queue but my slot was missed.",
	}
	mutate(body)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return string(raw)
}

func TestSubmitPublicDispute_AbsentOrNullRequiredFieldIsTypedValidationFailed(t *testing.T) {
	fields := []string{"student_id", "submitted_email", "event_id", "category_id", "od_date", "slot", "claimed_coordinator_name", "student_note"}

	for _, field := range fields {
		for _, shape := range []string{"absent", "null"} {
			t.Run(field+"_"+shape, func(t *testing.T) {
				svc := &countingPublicService{}
				router := newBoundsRouter(svc)
				body := completeBody(t, func(m map[string]any) {
					if shape == "absent" {
						delete(m, field)
						return
					}
					m[field] = nil
				})

				recorder := postBody(t, router, body)

				if recorder.Code != http.StatusBadRequest {
					t.Fatalf("[P0] %s %s returned %d, want 400. AC 4 requires every required field bounded at "+
						"the edge; a zero-valued uuid or date reaches the database and comes back as "+
						"EVENT_NOT_FOUND or an unmapped NOT NULL violation instead", field, shape, recorder.Code)
				}
				var envelope contracts.ValidationError
				if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
					t.Fatalf("[P0] %s %s body is not a ValidationError: %v (%s)", field, shape, err, recorder.Body.String())
				}
				if envelope.Code != contracts.ValidationErrorCodeVALIDATIONFAILED {
					t.Errorf("[P0] %s %s code = %q, want VALIDATION_FAILED", field, shape, envelope.Code)
				}
				if len(envelope.Details) == 0 {
					t.Fatalf("[P0] %s %s carried no details; AC 4 requires a typed, field-named refusal", field, shape)
				}
				if envelope.Details[0].Field != field {
					t.Errorf("[P0] %s %s named field %q, want %q — a refusal that names the wrong field is not "+
						"actionable, and map-ordered validation made this non-deterministic",
						field, shape, envelope.Details[0].Field, field)
				}
				if svc.submits != 0 {
					t.Errorf("[P0] %s %s reached the service (%d calls); it must be refused before any query",
						field, shape, svc.submits)
				}
			})
		}
	}
}

// The nil UUID is "absent" spelled differently: it parses, so every uuid.Parse
// check passes, and pgtype maps it to NULL.
func TestSubmitPublicDispute_NilUUIDIsRefusedNotCarriedToTheDatabase(t *testing.T) {
	for _, field := range []string{"student_id", "event_id", "category_id"} {
		t.Run(field, func(t *testing.T) {
			svc := &countingPublicService{}
			router := newBoundsRouter(svc)
			body := completeBody(t, func(m map[string]any) {
				m[field] = "00000000-0000-0000-0000-000000000000"
			})

			recorder := postBody(t, router, body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("[P0] nil %s returned %d, want 400", field, recorder.Code)
			}
			if svc.submits != 0 {
				t.Errorf("[P0] nil %s reached the service; NULL would then hit a NOT NULL constraint as a 500", field)
			}
		})
	}
}

// The body must be bounded before the decoder allocates. Every other POST in
// this codebase wraps with MaxBytesReader; this is the one route anyone can
// reach, and the rate limiter counts requests, not bytes.
func TestSubmitPublicDispute_OversizedBodyIsRefusedWithoutReachingTheService(t *testing.T) {
	svc := &countingPublicService{}
	router := newBoundsRouter(svc)

	var oversized bytes.Buffer
	oversized.WriteString(`{"student_note":"`)
	oversized.Write(bytes.Repeat([]byte("a"), maxDisputeRequestBytes+1024))
	oversized.WriteString(`"}`)

	recorder := postBody(t, router, oversized.String())

	if recorder.Code == http.StatusCreated {
		t.Fatalf("[P0] an oversized body was accepted")
	}
	if svc.submits != 0 {
		t.Errorf("[P0] an oversized body reached the service; it must be cut off at the reader")
	}
}
