package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	eventsvc "github.com/Shruhath/OD-Amrita/api/internal/service/event"
)

type Service interface {
	Branches(context.Context) ([]eventsvc.Branch, error)
	List(context.Context, eventsvc.ListInput) (eventsvc.Page, error)
	Create(context.Context, eventsvc.Actor, eventsvc.CreateInput) (eventsvc.Event, error)
	Archive(context.Context, eventsvc.Actor, string) (eventsvc.Event, error)
}

func ListBranchesHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		branches, err := svc.Branches(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		data := make([]contracts.BranchSummary, 0, len(branches))
		for _, branch := range branches {
			mapped, mapErr := branchContract(branch)
			if mapErr != nil {
				fail(c, mapErr)
				return
			}
			data = append(data, mapped)
		}
		c.JSON(http.StatusOK, contracts.BranchCollection{Data: data})
	}
}

func ListEventsHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var params contracts.ListAdminEventsParams
		if err := c.ShouldBindQuery(&params); err != nil {
			fail(c, requestValidation("$query", "invalid_format", "Query parameters are invalid."))
			return
		}
		input := eventsvc.ListInput{}
		if params.Status != nil {
			input.Status = eventsvc.Status(*params.Status)
		}
		if params.Page != nil {
			input.Page = *params.Page
		}
		if params.PageSize != nil {
			input.PageSize = *params.PageSize
		}
		page, err := svc.List(c.Request.Context(), input)
		if err != nil {
			fail(c, err)
			return
		}
		data := make([]contracts.Event, 0, len(page.Data))
		for _, item := range page.Data {
			mapped, mapErr := eventContract(item)
			if mapErr != nil {
				fail(c, mapErr)
				return
			}
			data = append(data, mapped)
		}
		c.JSON(http.StatusOK, contracts.EventCollection{
			Data: data, Page: page.Page, PageSize: page.PageSize, Total: int(page.Total),
		})
	}
}

func CreateEventHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body contracts.CreateEventRequest
		if err := bindCreateRequest(c, &body); err != nil {
			fail(c, err)
			return
		}
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		branchIDs := make([]string, len(body.BranchIds))
		for index, id := range body.BranchIds {
			branchIDs[index] = id.String()
		}
		created, err := svc.Create(c.Request.Context(), actor, eventsvc.CreateInput{
			Name: body.Name, EventDate: body.EventDate.Time.Format("2006-01-02"), BranchIDs: branchIDs,
		})
		if err != nil {
			fail(c, err)
			return
		}
		response, err := eventContract(created)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusCreated, response)
	}
}

type createRequestShape struct {
	EventDate json.RawMessage   `json:"event_date"`
	BranchIDs []json.RawMessage `json:"branch_ids"`
}

const maxCreateRequestBytes int64 = 1 << 20

// bindCreateRequest keeps transport validation field-addressable while the
// successful request still binds into the generated OpenAPI type. The first
// pass retains raw date/UUID values, because their generated unmarshallers
// otherwise collapse a field error into an undifferentiated JSON error.
func bindCreateRequest(c *gin.Context, body *contracts.CreateEventRequest) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCreateRequestBytes)
	var shape createRequestShape
	if err := c.ShouldBindBodyWith(&shape, binding.JSON); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return requestValidation("$", "maximum", "Request body must not exceed 1 MiB.")
		}
		return requestValidation("$", "invalid_json", "Request body must be valid JSON.")
	}
	if len(shape.EventDate) == 0 {
		return requestValidation("event_date", "required", "Event date is required.")
	}
	var date string
	if err := json.Unmarshal(shape.EventDate, &date); err != nil {
		return requestValidation("event_date", "invalid_format", "Event date must be a valid YYYY-MM-DD date.")
	}
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil || parsed.Format("2006-01-02") != date {
		return requestValidation("event_date", "invalid_format", "Event date must be a valid YYYY-MM-DD date.")
	}
	for index, raw := range shape.BranchIDs {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return requestValidation(fmt.Sprintf("branch_ids[%d]", index), "invalid_format", "Branch id must be a UUID.")
		}
		if _, err := uuid.Parse(value); err != nil {
			return requestValidation(fmt.Sprintf("branch_ids[%d]", index), "invalid_format", "Branch id must be a UUID.")
		}
	}
	if err := c.ShouldBindBodyWith(body, binding.JSON); err != nil {
		return requestValidation("$", "invalid_json", "Request body must be valid JSON.")
	}
	return nil
}

func ArchiveEventHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		archived, err := svc.Archive(c.Request.Context(), actor, c.Param("event_id"))
		if err != nil {
			fail(c, err)
			return
		}
		response, err := eventContract(archived)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

func actorFromContext(c *gin.Context) (eventsvc.Actor, error) {
	principal, ok := rbac.PrincipalFromContext(c)
	if !ok {
		return eventsvc.Actor{}, errors.New("event: principal missing from context")
	}
	return eventsvc.Actor{UserID: principal.UserID, Email: principal.Email, Role: principal.Role}, nil
}

func eventContract(value eventsvc.Event) (contracts.Event, error) {
	id, err := uuid.Parse(value.ID)
	if err != nil {
		return contracts.Event{}, fmt.Errorf("event: map id: %w", err)
	}
	if value.Status != eventsvc.StatusActive && value.Status != eventsvc.StatusArchived {
		return contracts.Event{}, fmt.Errorf("event: map invalid status %q", value.Status)
	}
	branches := make([]contracts.BranchSummary, 0, len(value.Branches))
	for _, branch := range value.Branches {
		mapped, mapErr := branchContract(branch)
		if mapErr != nil {
			return contracts.Event{}, mapErr
		}
		branches = append(branches, mapped)
	}
	result := contracts.Event{
		Id: id, Name: value.Name, Status: contracts.EventStatus(value.Status),
		Branches: branches, CreatedAt: value.CreatedAt, ArchivedAt: value.ArchivedAt,
	}
	date, err := time.Parse("2006-01-02", value.EventDate)
	if err != nil {
		return contracts.Event{}, fmt.Errorf("event: map date: %w", err)
	}
	result.EventDate.Time = date
	return result, nil
}

func branchContract(value eventsvc.Branch) (contracts.BranchSummary, error) {
	id, err := uuid.Parse(value.ID)
	if err != nil {
		return contracts.BranchSummary{}, fmt.Errorf("event: map branch id: %w", err)
	}
	return contracts.BranchSummary{Id: id, Code: value.Code, Name: value.Name}, nil
}

func requestValidation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code: contracts.ErrorCodeVALIDATIONFAILED, Message: "Event validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func fail(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}
