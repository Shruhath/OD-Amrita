// Package appsettings is the admin HTTP edge of the runtime switches.
//
// ADMIN ONLY, and mounted on the admin group rather than checking a role here:
// this switch changes what every coordinator's device will accept, so it belongs
// behind the same guard as the rest of the administrative surface rather than
// behind a role check somebody has to remember to write.
package appsettings

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	odrecord "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
)

// Service is the narrow surface this handler needs.
type Service interface {
	ExtendedSlotsEnabled(ctx context.Context) (bool, error)
	SetExtendedSlots(ctx context.Context, enabled bool, actorUserID string) (bool, time.Time, error)
}

func RegisterAdminRoutes(group gin.IRouter, svc Service) {
	group.GET("/settings/extended-slots", GetExtendedSlotsHandler(svc))
	group.PUT("/settings/extended-slots", SetExtendedSlotsHandler(svc))
}

func GetExtendedSlotsHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		enabled, err := svc.ExtendedSlotsEnabled(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, settingContract(enabled, time.Time{}))
	}
}

// extendedSlotsBody uses a POINTER so a missing `enabled` is distinguishable
// from `false`. A body of `{}` silently turning the switch off — which is what a
// value type would do — is the kind of write nobody would notice until a
// coordinator could not mark.
type extendedSlotsBody struct {
	Enabled *bool `json:"enabled"`
}

func SetExtendedSlotsHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body extendedSlotsBody
		if err := c.ShouldBindJSON(&body); err != nil {
			fail(c, validation("enabled", "invalid", "Request body must be JSON with an `enabled` boolean."))
			return
		}
		if body.Enabled == nil {
			fail(c, validation("enabled", "required", "`enabled` is required."))
			return
		}

		// The acting administrator is recorded on the row. A switch that changes
		// what the whole campus can do should not be anonymous.
		principal, _ := rbac.PrincipalFromContext(c)
		enabled, updatedAt, err := svc.SetExtendedSlots(c.Request.Context(), *body.Enabled, principal.UserID)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, settingContract(enabled, updatedAt))
	}
}

// settingContract sends the CEILING alongside the flag.
//
// So no client has to hold its own copy of what "on" means. A screen that
// hardcoded 20 would silently disagree with the server the day that number
// changed, and the disagreement would show up as a slot the picker offers and
// the API refuses.
func settingContract(enabled bool, updatedAt time.Time) contracts.ExtendedSlotsSetting {
	setting := contracts.ExtendedSlotsSetting{
		Enabled: enabled,
		MaxSlot: odrecord.SlotCount(enabled),
	}
	if !updatedAt.IsZero() {
		at := updatedAt
		setting.UpdatedAt = &at
	}
	return setting
}

func validation(field, code, message string) error {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Setting update validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func fail(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}
