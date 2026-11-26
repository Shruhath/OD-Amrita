package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcaudit "github.com/Shruhath/OD-Amrita/api/internal/service/audit"
	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
)

func TestListHandler_RendersPagedAuditEntries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	occurred := time.Date(2026, 9, 3, 12, 30, 0, 0, time.UTC)
	svc := &fakeService{page: svcaudit.Page{
		Page: 2, PageSize: 25, Total: 51,
		Data: []svcaudit.Entry{{
			ID:           "97000000-0000-4000-8000-000000000001",
			Action:       "app_setting.updated",
			ActorAccount: stringPtr("admin@amrita.edu"),
			ActorRole:    stringPtr("admin"),
			TargetTable:  stringPtr("app_settings"),
			TargetID:     stringPtr("extended_slots_enabled"),
			OccurredAt:   occurred,
			Payload:      map[string]any{"new_value": true},
		}},
	}}

	router := gin.New()
	router.GET("/audit-log", func(c *gin.Context) {
		c.Set("rbac.principal", rbac.Principal{UserID: "admin-id", Email: "admin@amrita.edu", Role: "admin"})
		ListHandler(svc)(c)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/audit-log?action=app_setting.updated&page=2&page_size=25", nil)

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body contracts.AuditLogPage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if svc.input.Action != "app_setting.updated" || svc.input.Page != 2 || svc.input.PageSize != 25 {
		t.Fatalf("handler input = %+v", svc.input)
	}
	if body.Total != 51 || len(body.Data) != 1 {
		t.Fatalf("body = %+v", body)
	}
	entry := body.Data[0]
	if entry.Id == nil || entry.Id.String() != "97000000-0000-4000-8000-000000000001" ||
		entry.TargetTable == nil || *entry.TargetTable != "app_settings" ||
		entry.ActorEmail == nil || string(*entry.ActorEmail) != "admin@amrita.edu" {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Payload == nil || (*entry.Payload)["new_value"] != true {
		t.Fatalf("payload = %#v", entry.Payload)
	}
}

type fakeService struct {
	input svcaudit.ListInput
	page  svcaudit.Page
}

func (f *fakeService) List(_ context.Context, _ svcaudit.Actor, input svcaudit.ListInput) (svcaudit.Page, error) {
	f.input = input
	return f.page, nil
}

func stringPtr(value string) *string {
	return &value
}
