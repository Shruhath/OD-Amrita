package audit

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcaudit "github.com/Shruhath/OD-Amrita/api/internal/service/audit"
	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
)

type Service interface {
	List(context.Context, svcaudit.Actor, svcaudit.ListInput) (svcaudit.Page, error)
}

func RegisterAdminRoutes(group gin.IRouter, svc Service) {
	group.GET("/audit-log", ListHandler(svc))
}

func ListHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		var params contracts.ListAdminAuditLogParams
		if err := c.ShouldBindQuery(&params); err != nil {
			fail(c, err)
			return
		}
		page, err := svc.List(c.Request.Context(), actor, inputFromParams(params))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, pageContract(page))
	}
}

func inputFromParams(params contracts.ListAdminAuditLogParams) svcaudit.ListInput {
	input := svcaudit.ListInput{}
	if params.Action != nil {
		input.Action = *params.Action
	}
	if params.ActorAccount != nil {
		input.ActorAccount = *params.ActorAccount
	}
	if params.TargetTable != nil {
		input.TargetTable = *params.TargetTable
	}
	if params.TargetId != nil {
		input.TargetID = *params.TargetId
	}
	if params.Page != nil {
		input.Page = *params.Page
	}
	if params.PageSize != nil {
		input.PageSize = *params.PageSize
	}
	return input
}

func pageContract(page svcaudit.Page) contracts.AuditLogPage {
	data := make([]contracts.AuditLogEntry, 0, len(page.Data))
	for _, entry := range page.Data {
		data = append(data, entryContract(entry))
	}
	return contracts.AuditLogPage{Data: data, Page: page.Page, PageSize: page.PageSize, Total: page.Total}
}

func entryContract(entry svcaudit.Entry) contracts.AuditLogEntry {
	id := openapi_types.UUID(uuid.MustParse(entry.ID))
	var email *openapi_types.Email
	if entry.ActorAccount != nil {
		value := openapi_types.Email(*entry.ActorAccount)
		email = &value
	}
	return contracts.AuditLogEntry{
		Id:          &id,
		Action:      entry.Action,
		ActorEmail:  email,
		ActorRole:   entry.ActorRole,
		TargetTable: entry.TargetTable,
		TargetId:    entry.TargetID,
		OccurredAt:  entry.OccurredAt,
		Payload:     &entry.Payload,
	}
}

func actorFromContext(c *gin.Context) (svcaudit.Actor, error) {
	principal, ok := rbac.PrincipalFromContext(c)
	if !ok {
		return svcaudit.Actor{}, rbac.ErrPrincipalNotFound
	}
	return svcaudit.Actor{UserID: principal.UserID, Email: principal.Email, Role: principal.Role}, nil
}

func fail(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}
