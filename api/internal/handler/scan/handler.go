package scan

import (
	"context"
	"net/http"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
	scansvc "github.com/Shruhath/OD-Amrita/api/internal/service/scan"
	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"
	"github.com/gin-gonic/gin"
)

const maxBatchItems = 50

type Service interface {
	Submit(context.Context, scansvc.Actor, scansvc.Item) (scansvc.Outcome, error)
}

func RegisterRoutes(group gin.IRouter, svc Service) {
	group.POST("/scans", BatchHandler(svc))
}

func BatchHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := rbac.PrincipalFromContext(c)
		if !ok {
			fail(c, domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Authenticated principal is required."})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var request contracts.ScanBatchRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			fail(c, domain.DomainError{Code: contracts.ErrorCodeVALIDATIONFAILED, Message: "Scan batch must be valid JSON.", Details: []contracts.ValidationErrorDetail{{Field: "$", Code: "invalid_json", Message: "Request body must be valid JSON."}}})
			return
		}
		if len(request.Scans) == 0 || len(request.Scans) > maxBatchItems {
			fail(c, domain.DomainError{Code: contracts.ErrorCodeVALIDATIONFAILED, Message: "Scan batch size is invalid.", Details: []contracts.ValidationErrorDetail{{Field: "scans", Code: "maximum", Message: "Submit between 1 and 50 scans."}}})
			return
		}

		actor := odsvc.Actor{UserID: principal.UserID, Email: principal.Email, Role: principal.Role}
		response := contracts.ScanBatchResponse{Results: make([]contracts.ScanResult, 0, len(request.Scans))}
		for _, item := range request.Scans {
			outcome, err := svc.Submit(c.Request.Context(), actor, scansvc.Item{
				ID: item.Id, EventID: item.EventId, CategoryID: item.CategoryId,
				Method: string(item.Method), StudentID: item.StudentId,
				CardToken: item.CardToken, CardVersion: item.CardVersion,
			})
			if err != nil {
				fail(c, err)
				return
			}
			result := contracts.ScanResult{Id: item.Id, Status: contracts.ScanResultStatus(outcome.Status)}
			if outcome.Reason != "" {
				result.Reason = &outcome.Reason
			}
			response.Results = append(response.Results, result)
		}
		c.JSON(http.StatusAccepted, response)
	}
}

func fail(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}
