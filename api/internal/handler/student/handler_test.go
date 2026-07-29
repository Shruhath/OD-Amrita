package student

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	studentsvc "github.com/Shruhath/OD-Amrita/api/internal/service/student"
)

func TestImportStudentsHandler_BindsMultipartFileAndRendersResult(t *testing.T) {
	svc := &fakeImportService{result: studentsvc.Result{
		ImportID:      "77777777-7777-4777-8777-777777777777",
		TotalRows:     1,
		InsertedRows:  1,
		DatabaseCount: 1,
	}}
	router := testRouter(svc)
	body, contentType := multipartBody(t, "file", "students.csv", "Roll No,Name,Branch,Section,Batch\nCB.SC.U4CSE24001,Name,CSE,A,2024\n")

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/students/import", body)
	req.Header.Set("Content-Type", contentType)
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if svc.fileName != "students.csv" {
		t.Errorf("filename = %q", svc.fileName)
	}
	if !strings.Contains(svc.content, "CB.SC.U4CSE24001") {
		t.Errorf("service content = %q", svc.content)
	}
	if !strings.Contains(recorder.Body.String(), `"inserted_rows":1`) {
		t.Errorf("response body = %s", recorder.Body.String())
	}
}

func TestImportStudentsHandler_RequiresMultipartFile(t *testing.T) {
	router := testRouter(&fakeImportService{})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/students/import", strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=empty")
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "VALIDATION_FAILED") {
		t.Errorf("body = %s, want validation envelope", recorder.Body.String())
	}
}

type fakeImportService struct {
	result   studentsvc.Result
	fileName string
	content  string
}

func (f *fakeImportService) Import(ctx context.Context, actor studentsvc.Actor, input studentsvc.ImportInput) (studentsvc.Result, error) {
	f.fileName = input.FileName
	data, err := io.ReadAll(input.Content)
	if err != nil {
		return studentsvc.Result{}, err
	}
	f.content = string(data)
	return f.result, nil
}

func testRouter(svc Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errormw.Handler())
	router.Use(func(c *gin.Context) {
		c.Set("rbac.principal", rbac.Principal{UserID: "90000000-0000-4000-8000-000000000001", Email: "admin@amrita.edu", Role: "admin"})
		c.Next()
	})
	admin := router.Group("/api/v1/admin")
	RegisterAdminRoutes(admin, svc)
	return router
}

func multipartBody(t *testing.T, field, filename, content string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body, writer.FormDataContentType()
}
