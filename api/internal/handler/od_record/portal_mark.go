package odrecord

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
)

type PortalMarkService interface {
	Events(context.Context, marking.Actor) ([]marking.Event, error)
	Categories(context.Context, marking.Actor, string) ([]marking.Category, error)
}

func RegisterPortalMarkRoutes(group gin.IRouter, svc PortalMarkService) {
	group.GET("/portal/mark/events", PortalMarkEventsHandler(svc))
	group.GET("/portal/mark/events/:event_id/categories", PortalMarkCategoriesHandler(svc))
}

func PortalMarkEventsHandler(svc PortalMarkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := portalMarkActor(c)
		if err != nil {
			fail(c, err)
			return
		}
		page, pageSize, err := portalPagination(c)
		if err != nil {
			fail(c, err)
			return
		}
		events, err := svc.Events(c.Request.Context(), actor)
		if err != nil {
			fail(c, err)
			return
		}
		slice := pageWindow(len(events), page, pageSize)
		data := make([]contracts.Event, 0, slice.to-slice.from)
		for _, event := range events[slice.from:slice.to] {
			rendered, convErr := eventContract(event)
			if convErr != nil {
				fail(c, convErr)
				return
			}
			data = append(data, rendered)
		}
		c.JSON(http.StatusOK, contracts.EventCollection{Data: data, Page: page, PageSize: pageSize, Total: len(events)})
	}
}

func PortalMarkCategoriesHandler(svc PortalMarkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := portalMarkActor(c)
		if err != nil {
			fail(c, err)
			return
		}
		eventID := strings.TrimSpace(c.Param("event_id"))
		if _, parseErr := uuid.Parse(eventID); parseErr != nil {
			fail(c, readValidation("event_id", "invalid_format", "event_id must be a UUID."))
			return
		}
		categories, err := svc.Categories(c.Request.Context(), actor, eventID)
		if err != nil {
			fail(c, err)
			return
		}
		data := make([]contracts.Category, 0, len(categories))
		for _, category := range categories {
			rendered, convErr := categoryContract(category)
			if convErr != nil {
				fail(c, convErr)
				return
			}
			data = append(data, rendered)
		}
		c.JSON(http.StatusOK, contracts.CategoryCollection{Data: data})
	}
}

func portalMarkActor(c *gin.Context) (marking.Actor, error) {
	actor, err := actorFromContext(c)
	if err != nil {
		return marking.Actor{}, err
	}
	if actor.Role != "teacher" && actor.Role != "admin" {
		return marking.Actor{}, domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Teacher or administrator access is required."}
	}
	return marking.Actor{UserID: actor.UserID, Email: actor.Email, Role: actor.Role}, nil
}

func portalPagination(c *gin.Context) (int, int, error) {
	page, err := boundedQueryInt(c, "page", 1, 1, maxEventPage)
	if err != nil {
		return 0, 0, err
	}
	pageSize, err := boundedQueryInt(c, "page_size", 20, 1, maxEventPageSize)
	if err != nil {
		return 0, 0, err
	}
	return page, pageSize, nil
}
