package odrecord

import (
	"context"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
)

type RecordManagementService interface {
	Correct(context.Context, odsvc.Actor, odsvc.CorrectionInput) (odsvc.ManagementRecord, error)
	Remove(context.Context, odsvc.Actor, string, string) (odsvc.ManagementRecord, error)
	Audit(context.Context, odsvc.Actor, string) ([]odsvc.AuditEntry, error)
}

// RegisterRecordManagementRoutes keeps teacher-reachable mutations on markAPI;
// mounting these on the admin group would reject every teacher before the
// branch-scoped service could decide authorization.
func RegisterRecordManagementRoutes(group gin.IRouter, svc RecordManagementService) {
	group.POST("/portal/records/:record_id/correction", CorrectionHandler(svc))
	group.POST("/portal/records/:record_id/removal", RemovalHandler(svc))
	group.GET("/portal/records/:record_id/audit", AuditHandler(svc))
}

func AuditHandler(svc RecordManagementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		entries, err := svc.Audit(c.Request.Context(), actor, strings.TrimSpace(c.Param("record_id")))
		if err != nil {
			fail(c, err)
			return
		}
		data := make([]contracts.AuditLogEntry, 0, len(entries))
		for _, entry := range entries {
			var email *openapi_types.Email
			if entry.ActorEmail != nil {
				value := openapi_types.Email(*entry.ActorEmail)
				email = &value
			}
			data = append(data, contracts.AuditLogEntry{Action: entry.Action, ActorEmail: email, ActorRole: entry.ActorRole, OccurredAt: entry.OccurredAt, Payload: &entry.Payload})
		}
		c.JSON(http.StatusOK, contracts.AuditLogEntryCollection{Data: data})
	}
}

func CorrectionHandler(svc RecordManagementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		var body contracts.RecordCorrectionRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			fail(c, requestValidation("$", "invalid_json", "Request body does not match the correction schema."))
			return
		}
		var category *string
		if body.CategoryId != nil {
			value := body.CategoryId.String()
			category = &value
		}
		var odDate *string
		if body.OdDate != nil {
			value := body.OdDate.String()
			odDate = &value
		}
		result, err := svc.Correct(c.Request.Context(), actor, odsvc.CorrectionInput{RecordID: strings.TrimSpace(c.Param("record_id")), ReasonNote: body.ReasonNote, Slot: body.Slot, CategoryID: category, ODDate: odDate})
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func RemovalHandler(svc RecordManagementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		var body contracts.RecordRemovalRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			fail(c, requestValidation("$", "invalid_json", "Request body does not match the removal schema."))
			return
		}
		result, err := svc.Remove(c.Request.Context(), actor, strings.TrimSpace(c.Param("record_id")), body.ReasonNote)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}
