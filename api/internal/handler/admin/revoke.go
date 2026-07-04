// Package admin holds admin-only HTTP handlers. Story 1.5c adds the forced-logout
// endpoint; broader admin surface (Epic 2 RBAC, event lifecycle) lands later.
package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	mwauth "github.com/Shruhath/OD-Amrita/api/internal/middleware/auth"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
)

// RevocationService is the narrow view the handler needs of the forced-logout
// service. *svcauth.RevocationService satisfies it; the handler depends on the
// behavior, not the concrete service.
type RevocationService interface {
	RevokeUserSessions(ctx context.Context, actor svcauth.VerifiedIdentity, targetUserID string, req svcauth.RevokeRequest) (svcauth.Revocation, error)
}

// RevokeSessionsHandler serves POST /api/v1/admin/users/{user_id}/sessions/revoke
// (Story 1.5c, DoD 7/8/10). It binds the request into the generated contract
// type, validates the field rules (400 VALIDATION_FAILED without touching the
// service on bad input), then delegates to the service, which enforces the narrow
// admin gate and records the revocation. The service's domain errors
// (RBAC_FORBIDDEN, VALIDATION_FAILED, …) are rendered by the central errormw.
func RevokeSessionsHandler(svc RevocationService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body contracts.RevokeSessionsRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			// Malformed JSON (or a non-UUID session_id that fails to bind) is a
			// client validation failure, not a 500.
			_ = c.Error(domain.DomainError{Code: contracts.ErrorCodeVALIDATIONFAILED, Message: "Request body is invalid."})
			c.Abort()
			return
		}

		req := svcauth.RevokeRequest{
			Scope:  svcauth.RevokeScope(body.Scope),
			Reason: body.Reason,
		}
		if body.SessionId != nil {
			req.SessionID = body.SessionId.String()
		}
		if err := req.Validate(); err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}

		// The auth middleware always sets the identity ahead of this handler; a
		// missing one means it was mounted without auth — fail closed (500).
		actor, ok := mwauth.IdentityFromContext(c)
		if !ok {
			_ = c.Error(errors.New("admin: identity missing from context"))
			c.Abort()
			return
		}

		rev, err := svc.RevokeUserSessions(c.Request.Context(), actor, c.Param("user_id"), req)
		if err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}

		c.JSON(http.StatusOK, contracts.SessionRevocationResult{
			RevocationId:   rev.ID,
			AlreadyRevoked: !rev.Inserted,
		})
	}
}
