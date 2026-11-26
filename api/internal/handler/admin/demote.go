package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcuserrole "github.com/Shruhath/OD-Amrita/api/internal/service/userrole"
)

// DemotionService is the narrow view the handler needs of the demotion
// service. *svcuserrole.Service satisfies it, and so does the cache-dropping
// wrapper cmd/api mounts.
type DemotionService interface {
	Demote(ctx context.Context, actor svcuserrole.Actor, targetUserID string) (svcuserrole.Result, error)
}

// DemoteUserHandler serves POST /api/v1/admin/users/{user_id}/demote. It takes
// no body. The acting principal is the one RequireAdmin loaded; the service
// re-checks the role and owns every refusal, rendered by the central errormw.
func DemoteUserHandler(svc DemotionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := rbac.PrincipalFromContext(c)
		if !ok {
			// Mounted without RequireAdmin ahead of it — fail closed (500).
			_ = c.Error(errors.New("admin: principal missing from context"))
			c.Abort()
			return
		}
		result, err := svc.Demote(c.Request.Context(), svcuserrole.Actor{
			UserID: principal.UserID, Email: principal.Email, Role: principal.Role,
		}, c.Param("user_id"))
		if err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}
		userID, err := uuid.Parse(result.UserID)
		if err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}
		c.JSON(http.StatusOK, contracts.UserDemotionResult{
			UserId:                        userID,
			Email:                         result.Email,
			PreviousRole:                  contracts.AuthenticatedIdentityRole(result.PreviousRole),
			Role:                          contracts.AuthenticatedIdentityRole(result.Role),
			Changed:                       result.Changed,
			RemovedCoordinatorAssignments: result.RemovedCoordinatorAssignments(),
			RemovedCategoryAssignments:    int(result.RemovedCategoryAssignments),
			RemovedTeacherBranches:        int(result.RemovedTeacherBranches),
			SessionsRevoked:               result.SessionsRevoked,
		})
	}
}
