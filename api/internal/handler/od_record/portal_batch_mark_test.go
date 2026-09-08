package odrecord

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
)

type batchMarkServiceStub struct {
	calls int
	input odsvc.BatchMarkInput
	err   error
}

func (s *batchMarkServiceStub) MarkBatch(_ context.Context, _ odsvc.Actor, input odsvc.BatchMarkInput) ([]odsvc.MarkResult, error) {
	s.calls++
	s.input = input
	return nil, s.err
}

func TestPortalBatchMarkHandlerRejectsMissingFieldsBeforeService(t *testing.T) {
	svc := &batchMarkServiceStub{}
	recorder := batchMarkRequest(t, svc, `{"event_id":"`+uuid.NewString()+`"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
	}
	var body contracts.ValidationError
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode validation response: %v", err)
	}
	if body.Code != contracts.ValidationErrorCodeVALIDATIONFAILED || len(body.Details) != 1 || body.Details[0].Field != "category_id" {
		t.Fatalf("validation response = %+v, want missing category_id", body)
	}
	if svc.calls != 0 {
		t.Fatalf("service calls=%d, want 0", svc.calls)
	}
}

func TestPortalBatchMarkHandlerRendersDetailedAndGenericConflicts(t *testing.T) {
	studentID := uuid.NewString()
	for _, tc := range []struct {
		name  string
		err   error
		check func(*testing.T, []byte)
	}{
		{
			name: "duplicate includes students",
			err:  odsvc.BatchDuplicateError{Students: []odsvc.Student{{StudentID: studentID, RollNo: "CB.SC.U4CSE24001", DisplayName: "Asha", Branch: "CSE", Section: "A", Batch: "2024"}}},
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var response contracts.BatchManualMarkConflict
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("decode duplicate conflict: %v", err)
				}
				if response.Code != contracts.BatchManualMarkConflictCodeSCANDUPLICATE || len(response.DuplicateStudents) != 1 || response.DuplicateStudents[0].StudentId.String() != studentID {
					t.Fatalf("duplicate response = %+v", response)
				}
			},
		},
		{
			name: "event state uses standard envelope",
			err:  domain.DomainError{Code: contracts.ErrorCodeEVENTARCHIVED, Message: "Event is archived."},
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var response contracts.Error
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("decode generic conflict: %v", err)
				}
				if response.Code != contracts.ErrorCodeEVENTARCHIVED {
					t.Fatalf("generic conflict = %+v", response)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &batchMarkServiceStub{err: tc.err}
			recorder := batchMarkRequest(t, svc, validBatchBody())
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s, want 409", recorder.Code, recorder.Body.String())
			}
			tc.check(t, recorder.Body.Bytes())
		})
	}
}

func batchMarkRequest(t *testing.T, svc PortalBatchMarkService, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errormw.Handler())
	router.Use(func(c *gin.Context) {
		c.Set("rbac.principal", rbac.Principal{UserID: uuid.NewString(), Email: "admin@amrita.edu", Role: "admin"})
		c.Next()
	})
	RegisterPortalBatchMarkRoutes(router.Group(""), svc)
	req := httptest.NewRequest(http.MethodPost, "/portal/mark/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func validBatchBody() string {
	return `{"event_id":"` + uuid.NewString() + `","category_id":"` + uuid.NewString() + `","student_ids":["` + uuid.NewString() + `"],"slot":4,"od_date":"2026-08-03","reason_note":"manual attendance"}`
}
