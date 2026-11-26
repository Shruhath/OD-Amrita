package student

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	studentsvc "github.com/Shruhath/OD-Amrita/api/internal/service/student"
)

func rosterListResult() studentsvc.ListResult {
	return studentsvc.ListResult{
		Students: []studentsvc.Student{
			{StudentID: "a1000000-0000-4000-8000-000000000001", RollNo: "CB.SC.U4CSE99001", DisplayName: "Ananya José", Branch: "CSE", Section: "A", Batch: "2024"},
			{StudentID: "a1000000-0000-4000-8000-000000000002", RollNo: "CB.SC.U4CSE99002", DisplayName: "Madhav Rao", Branch: "CSE", Section: "A", Batch: "2024"},
		},
		Page:     1,
		PageSize: 100,
		Total:    2,
	}
}

func testRouterWithRole(svc Service, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errormw.Handler())
	router.Use(func(c *gin.Context) {
		c.Set("rbac.principal", rbac.Principal{UserID: "90000000-0000-4000-8000-000000000001", Email: role + "@amrita.edu", Role: role})
		c.Next()
	})
	admin := router.Group("/api/v1/admin")
	RegisterAdminRoutes(admin, svc)
	return router
}

func getRoster(t *testing.T, router *gin.Engine, query string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/students"+query, nil))
	return recorder
}

func TestListStudentsHandler_RendersGeneratedCollection(t *testing.T) {
	svc := &fakeImportService{listResult: rosterListResult()}
	router := testRouter(svc)

	recorder := getRoster(t, router, "?branch_id=91000000-0000-4000-8000-000000000001&batch=2024&section=A&page=1&page_size=100")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var body contracts.StudentCollection
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode StudentCollection: %v", err)
	}
	if body.Page != 1 || body.PageSize != 100 || body.Total != 2 {
		t.Fatalf("page metadata = %+v, want page 1 size 100 total 2", body)
	}
	if len(body.Data) != 2 {
		t.Fatalf("data = %d rows, want 2", len(body.Data))
	}
	if body.Data[0].RollNo != "CB.SC.U4CSE99001" || body.Data[0].DisplayName != "Ananya José" {
		t.Errorf("first row = %+v, want the service's first student", body.Data[0])
	}

	got := *svc.listInput
	if got.BranchID != "91000000-0000-4000-8000-000000000001" || got.Batch != "2024" || got.Section != "A" {
		t.Errorf("service filters = %+v, want every query filter forwarded", got)
	}
	if got.Page != 1 || got.PageSize != 100 {
		t.Errorf("service pagination = page %d size %d, want 1/100", got.Page, got.PageSize)
	}
}

// The wire body must carry display fields only. This reads the raw JSON rather
// than the typed struct, because decoding into the generated type would discard
// any extra key and hide exactly the leak being tested.
func TestListStudentsHandler_ResponseCarriesNoPrivateFields(t *testing.T) {
	router := testRouter(&fakeImportService{listResult: rosterListResult()})

	recorder := getRoster(t, router, "")

	var raw struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw body: %v", err)
	}
	if len(raw.Data) == 0 {
		t.Fatal("no rows to inspect")
	}
	want := map[string]bool{
		"student_id": true, "roll_no": true, "display_name": true,
		"branch": true, "section": true, "batch": true,
	}
	for _, row := range raw.Data {
		for key := range row {
			if !want[key] {
				t.Errorf("roster row exposes unexpected field %q", key)
			}
		}
	}
}

func TestListStudentsHandler_RejectsNonAdminBeforeService(t *testing.T) {
	for _, role := range []string{"student", "teacher", "coordinator"} {
		t.Run(role, func(t *testing.T) {
			svc := &fakeImportService{listResult: rosterListResult()}
			router := testRouterWithRole(svc, role)

			recorder := getRoster(t, router, "?page=1&page_size=100")

			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, body=%s, want 403", recorder.Code, recorder.Body.String())
			}
			if svc.listCalls != 0 {
				t.Fatalf("service was called %d times for role %q, want 0", svc.listCalls, role)
			}
		})
	}
}

// RBAC must be answered before query parsing. A non-admin sending a malformed
// page must see 403, never the 400 that would confirm the endpoint exists and
// how it validates.
func TestListStudentsHandler_RejectsNonAdminBeforeQueryValidation(t *testing.T) {
	svc := &fakeImportService{listResult: rosterListResult()}
	router := testRouterWithRole(svc, "student")

	recorder := getRoster(t, router, "?page=abc&page_size=-3&branch_id=nope")

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body=%s, want 403 ahead of validation", recorder.Code, recorder.Body.String())
	}
	if svc.listCalls != 0 {
		t.Fatalf("service was called despite a forbidden caller")
	}
}

func TestListStudentsHandler_RejectsMalformedQueryWithTypedDetails(t *testing.T) {
	cases := []struct {
		name  string
		query string
		field string
		code  string
	}{
		{"non numeric page", "?page=abc", "page", "invalid_format"},
		{"non numeric page size", "?page_size=many", "page_size", "invalid_format"},
		{"zero page", "?page=0", "page", "minimum"},
		{"negative page size", "?page_size=-1", "page_size", "minimum"},
		{"branch id not a uuid", "?branch_id=not-a-uuid", "branch_id", "invalid_format"},
	}
	// page_size above the 100 maximum is a SERVICE-owned bound, so it is
	// asserted in TestList_RejectsPaginationAndFilterBounds rather than here —
	// a fake service cannot exhibit a rule the real service owns.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeImportService{listResult: rosterListResult()}
			router := testRouter(svc)

			recorder := getRoster(t, router, tc.query)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body=%s, want 400", recorder.Code, recorder.Body.String())
			}
			var body contracts.ValidationError
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode ValidationError: %v", err)
			}
			if len(body.Details) != 1 || body.Details[0].Field != tc.field || body.Details[0].Code != tc.code {
				t.Fatalf("details = %+v, want %s:%s", body.Details, tc.field, tc.code)
			}
		})
	}
}

func TestListStudentsHandler_OmittedFiltersReachServiceAsUnfiltered(t *testing.T) {
	svc := &fakeImportService{listResult: rosterListResult()}
	router := testRouter(svc)

	if code := getRoster(t, router, "").Code; code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	got := *svc.listInput
	if got.BranchID != "" || got.Batch != "" || got.Section != "" {
		t.Errorf("filters = %+v, want empty when omitted", got)
	}
	if got.Page != 0 || got.PageSize != 0 {
		t.Errorf("pagination = page %d size %d, want zero so the service applies its defaults", got.Page, got.PageSize)
	}
}

func TestListStudentsHandler_EmptyRosterRendersEmptyArray(t *testing.T) {
	router := testRouter(&fakeImportService{listResult: studentsvc.ListResult{
		Students: []studentsvc.Student{},
		Page:     1,
		PageSize: 100,
	}})

	recorder := getRoster(t, router, "")

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if string(raw["data"]) != "[]" {
		t.Fatalf("data = %s, want [] so the client never has to guard against null", raw["data"])
	}
}

func TestListStudentsHandler_PropagatesServiceValidationEnvelope(t *testing.T) {
	router := testRouter(&fakeImportService{listErr: domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Student roster validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: "section", Code: "maximum", Message: "section must not exceed 32 characters."}},
	}})

	recorder := getRoster(t, router, "?section=x")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s, want 400", recorder.Code, recorder.Body.String())
	}
	var body contracts.ValidationError
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ValidationError: %v", err)
	}
	if len(body.Details) != 1 || body.Details[0].Field != "section" {
		t.Fatalf("details = %+v, want the service envelope preserved", body.Details)
	}
}
