package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
	Get(context.Context, eventsvc.Actor, string) (eventsvc.Event, error)
	Create(context.Context, eventsvc.Actor, eventsvc.CreateInput) (eventsvc.Event, error)
	Archive(context.Context, eventsvc.Actor, string) (eventsvc.Event, error)
	ListCategories(context.Context, eventsvc.Actor, string) ([]eventsvc.Category, error)
	CreateCategory(context.Context, eventsvc.Actor, eventsvc.CreateCategoryInput) (eventsvc.Category, error)
	UpdateCategoryVisibility(context.Context, eventsvc.Actor, eventsvc.UpdateCategoryVisibilityInput) (eventsvc.Category, error)
	ListCoordinatorAssignments(context.Context, eventsvc.Actor, string) ([]eventsvc.CoordinatorAssignment, error)
	CreateCoordinatorAssignment(context.Context, eventsvc.Actor, eventsvc.CreateCoordinatorAssignmentInput) (eventsvc.CoordinatorAssignment, error)
	UpdateCoordinatorCategoryAccess(context.Context, eventsvc.Actor, eventsvc.UpdateCoordinatorCategoryAccessInput) (eventsvc.CoordinatorAssignment, error)
	RemoveCoordinatorAssignment(context.Context, eventsvc.Actor, eventsvc.RemoveCoordinatorAssignmentInput) error
	ListCaptureCredits(context.Context, eventsvc.Actor, string) (eventsvc.CaptureCreditSummary, error)
	UpdateCoordinatorCaptureCreditLimit(context.Context, eventsvc.Actor, eventsvc.CaptureCreditLimitInput) (eventsvc.CaptureCreditUsage, error)
	ResetCaptureCredits(context.Context, eventsvc.Actor, string, string) (eventsvc.CaptureCreditUsage, error)
}

// RegisterAdminRoutes mounts every admin event and category route on an
// already-guarded group.
//
// It exists because the routes were previously hand-wired TWICE — in cmd/api and
// again in test/portal — and a route added to one but forgotten in the other
// 404s only in the browser integration job, where it reads as a frontend bug.
// One registration function means the acceptance harness cannot serve a
// different route set than production.
func RegisterAdminRoutes(group gin.IRouter, svc Service) {
	group.GET("/branches", ListBranchesHandler(svc))
	group.GET("/events", ListEventsHandler(svc))
	group.POST("/events", CreateEventHandler(svc))
	// The item read. Every per-event surface needs it; without it a client has to
	// scan the paginated collection and filter locally.
	group.GET("/events/:event_id", GetEventHandler(svc))
	group.POST("/events/:event_id/archive", ArchiveEventHandler(svc))
	// Categories are nested under their event: a category has no identity
	// without one, and nesting makes the lifecycle check fall out of the path.
	group.GET("/events/:event_id/categories", ListCategoriesHandler(svc))
	group.POST("/events/:event_id/categories", CreateCategoryHandler(svc))
	group.PUT("/events/:event_id/categories/:category_id/visibility", UpdateCategoryVisibilityHandler(svc))
	group.GET("/events/:event_id/coordinators", ListCoordinatorAssignmentsHandler(svc))
	group.POST("/events/:event_id/coordinators", CreateCoordinatorAssignmentHandler(svc))
	group.PUT("/events/:event_id/coordinators/:user_id/categories", UpdateCoordinatorCategoryAccessHandler(svc))
	group.DELETE("/events/:event_id/coordinators/:user_id", RemoveCoordinatorAssignmentHandler(svc))
	group.GET("/events/:event_id/capture-credits", ListCaptureCreditsHandler(svc))
	group.PUT("/events/:event_id/capture-credits/coordinators/:user_id/limit", UpdateCoordinatorCaptureCreditLimitHandler(svc))
	group.POST("/events/:event_id/capture-credits/coordinators/:user_id/reset", ResetCaptureCreditsHandler(svc))
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
		// end_date is OPTIONAL in the contract, so oapi-codegen emits it as a
		// pointer. Nil is passed straight through as nil: the service defaults it
		// to the start date, and dereferencing here would be the nil-deref 0.4's
		// review found on exactly this shape.
		var endDate *string
		if body.EndDate != nil {
			value := body.EndDate.Time.Format("2006-01-02")
			endDate = &value
		}
		created, err := svc.Create(c.Request.Context(), actor, eventsvc.CreateInput{
			Name: body.Name, EventDate: body.EventDate.Time.Format("2006-01-02"), EndDate: endDate, BranchIDs: branchIDs,
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
	EndDate   json.RawMessage   `json:"end_date"`
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
	// end_date is optional, so an ABSENT key is legal and means a one-day event.
	// A key that is present must still be a well-formed date string: a JSON null,
	// a number, or "2026-13-45" are all a client sending something it meant, and
	// the generated unmarshaller would collapse each of them into an
	// undifferentiated JSON error rather than a failure on this field.
	if len(shape.EndDate) > 0 {
		var endDate string
		if err := json.Unmarshal(shape.EndDate, &endDate); err != nil {
			return requestValidation("end_date", "invalid_format", "End date must be a valid YYYY-MM-DD date.")
		}
		parsedEnd, err := time.Parse("2006-01-02", endDate)
		if err != nil || parsedEnd.Format("2006-01-02") != endDate {
			return requestValidation("end_date", "invalid_format", "End date must be a valid YYYY-MM-DD date.")
		}
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

func GetEventHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		found, err := svc.Get(c.Request.Context(), actor, c.Param("event_id"))
		if err != nil {
			fail(c, err)
			return
		}
		response, err := eventContract(found)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, response)
	}
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

func ListCoordinatorAssignmentsHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		rows, err := svc.ListCoordinatorAssignments(c.Request.Context(), actor, c.Param("event_id"))
		if err != nil {
			fail(c, err)
			return
		}
		data := make([]contracts.CoordinatorAssignment, 0, len(rows))
		for _, row := range rows {
			mapped, mapErr := coordinatorAssignmentContract(row)
			if mapErr != nil {
				fail(c, mapErr)
				return
			}
			data = append(data, mapped)
		}
		c.JSON(http.StatusOK, contracts.CoordinatorAssignmentCollection{Data: data})
	}
}

func CreateCoordinatorAssignmentHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body contracts.CreateCoordinatorAssignmentRequest
		if err := bindCoordinatorAssignmentRequest(c, &body); err != nil {
			fail(c, err)
			return
		}
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		created, err := svc.CreateCoordinatorAssignment(c.Request.Context(), actor, eventsvc.CreateCoordinatorAssignmentInput{
			EventID:     c.Param("event_id"),
			Email:       body.Email,
			CategoryIDs: uuidStrings(body.CategoryIds),
		})
		if err != nil {
			fail(c, err)
			return
		}
		response, err := coordinatorAssignmentContract(created)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusCreated, response)
	}
}

func ListCaptureCreditsHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		summary, err := svc.ListCaptureCredits(c.Request.Context(), actor, c.Param("event_id"))
		if err != nil {
			fail(c, err)
			return
		}
		response, err := captureCreditSummaryContract(summary)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

func UpdateCoordinatorCaptureCreditLimitHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body contracts.UpdateCaptureCreditLimitRequest
		if err := bindCaptureCreditLimitRequest(c, &body); err != nil {
			fail(c, err)
			return
		}
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		updated, err := svc.UpdateCoordinatorCaptureCreditLimit(c.Request.Context(), actor, eventsvc.CaptureCreditLimitInput{
			EventID: c.Param("event_id"),
			UserID:  c.Param("user_id"),
			Limit:   body.Limit,
		})
		if err != nil {
			fail(c, err)
			return
		}
		response, err := captureCreditUsageContract(updated)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

func UpdateCoordinatorCategoryAccessHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body contracts.UpdateCategoryAccessRequest
		if err := bindCategoryAccessRequest(c, &body, coordinatorRequestValidation); err != nil {
			fail(c, err)
			return
		}
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		categoryIDs := make([]string, len(body.CategoryIds))
		for index, id := range body.CategoryIds {
			categoryIDs[index] = id.String()
		}
		updated, err := svc.UpdateCoordinatorCategoryAccess(c.Request.Context(), actor, eventsvc.UpdateCoordinatorCategoryAccessInput{
			EventID: c.Param("event_id"), UserID: c.Param("user_id"), CategoryIDs: categoryIDs,
		})
		if err != nil {
			fail(c, err)
			return
		}
		response, err := coordinatorAssignmentContract(updated)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

// RemoveCoordinatorAssignmentHandler takes one coordinator off one event and
// answers 204. The ids are validated by the service so the 400 carries the same
// typed details as the sibling category-access update.
func RemoveCoordinatorAssignmentHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		if err := svc.RemoveCoordinatorAssignment(c.Request.Context(), actor, eventsvc.RemoveCoordinatorAssignmentInput{
			EventID: c.Param("event_id"), UserID: c.Param("user_id"),
		}); err != nil {
			fail(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

type captureCreditConfigRequestShape struct {
	Limit json.RawMessage `json:"limit"`
}

func bindCaptureCreditLimitRequest(c *gin.Context, body *contracts.UpdateCaptureCreditLimitRequest) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCreateRequestBytes)
	var shape captureCreditConfigRequestShape
	if err := c.ShouldBindBodyWith(&shape, binding.JSON); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return requestValidation("$", "maximum", "Request body must not exceed 1 MiB.")
		}
		return requestValidation("$", "invalid_json", "Request body must be valid JSON.")
	}
	if len(shape.Limit) == 0 {
		return requestValidation("limit", "required", "Fallback credit cap is required.")
	}
	if strings.TrimSpace(string(shape.Limit)) == "null" {
		return requestValidation("limit", "invalid_format", "Fallback credit cap must be a whole number.")
	}
	var limit int
	if err := json.Unmarshal(shape.Limit, &limit); err != nil {
		return requestValidation("limit", "invalid_format", "Fallback credit cap must be a whole number.")
	}
	if err := c.ShouldBindBodyWith(body, binding.JSON); err != nil {
		return requestValidation("$", "invalid_json", "Request body must be valid JSON.")
	}
	return nil
}

func ResetCaptureCreditsHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		usage, err := svc.ResetCaptureCredits(c.Request.Context(), actor, c.Param("event_id"), c.Param("user_id"))
		if err != nil {
			fail(c, err)
			return
		}
		response, err := captureCreditUsageContract(usage)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

type coordinatorAssignmentRequestShape struct {
	Email       json.RawMessage   `json:"email"`
	CategoryIDs []json.RawMessage `json:"category_ids"`
}

type categoryAccessRequestShape struct {
	CategoryIDs []json.RawMessage `json:"category_ids"`
}

func bindCoordinatorAssignmentRequest(c *gin.Context, body *contracts.CreateCoordinatorAssignmentRequest) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCreateRequestBytes)
	var shape coordinatorAssignmentRequestShape
	if err := c.ShouldBindBodyWith(&shape, binding.JSON); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return coordinatorRequestValidation("$", "maximum", "Request body must not exceed 1 MiB.")
		}
		return coordinatorRequestValidation("$", "invalid_json", "Request body must be valid JSON.")
	}
	if len(shape.Email) == 0 {
		return coordinatorRequestValidation("email", "required", "Coordinator email is required.")
	}
	var email string
	if err := json.Unmarshal(shape.Email, &email); err != nil {
		return coordinatorRequestValidation("email", "invalid_format", "Coordinator email must be a string.")
	}
	if len(shape.CategoryIDs) == 0 {
		return coordinatorRequestValidation("category_ids", "required", "Select at least one category.")
	}
	for index, raw := range shape.CategoryIDs {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return coordinatorRequestValidation(fmt.Sprintf("category_ids[%d]", index), "invalid_format", "Category id must be a UUID.")
		}
		if _, err := uuid.Parse(value); err != nil {
			return coordinatorRequestValidation(fmt.Sprintf("category_ids[%d]", index), "invalid_format", "Category id must be a UUID.")
		}
	}
	if err := c.ShouldBindBodyWith(body, binding.JSON); err != nil {
		return coordinatorRequestValidation("$", "invalid_json", "Request body must be valid JSON.")
	}
	return nil
}

func bindCategoryAccessRequest(
	c *gin.Context,
	body *contracts.UpdateCategoryAccessRequest,
	fieldError func(string, string, string) domain.DomainError,
) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCreateRequestBytes)
	var shape categoryAccessRequestShape
	if err := c.ShouldBindBodyWith(&shape, binding.JSON); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fieldError("$", "maximum", "Request body must not exceed 1 MiB.")
		}
		return fieldError("$", "invalid_json", "Request body must be valid JSON.")
	}
	if len(shape.CategoryIDs) == 0 {
		return fieldError("category_ids", "required", "Select at least one category.")
	}
	for index, raw := range shape.CategoryIDs {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return fieldError(fmt.Sprintf("category_ids[%d]", index), "invalid_format", "Category id must be a UUID.")
		}
		if _, err := uuid.Parse(value); err != nil {
			return fieldError(fmt.Sprintf("category_ids[%d]", index), "invalid_format", "Category id must be a UUID.")
		}
	}
	if err := c.ShouldBindBodyWith(body, binding.JSON); err != nil {
		return fieldError("$", "invalid_json", "Request body must be valid JSON.")
	}
	return nil
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for index, id := range ids {
		out[index] = id.String()
	}
	return out
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
	endDate, err := time.Parse("2006-01-02", value.EndDate)
	if err != nil {
		return contracts.Event{}, fmt.Errorf("event: map end date: %w", err)
	}
	result.EndDate.Time = endDate
	return result, nil
}

func branchContract(value eventsvc.Branch) (contracts.BranchSummary, error) {
	id, err := uuid.Parse(value.ID)
	if err != nil {
		return contracts.BranchSummary{}, fmt.Errorf("event: map branch id: %w", err)
	}
	return contracts.BranchSummary{Id: id, Code: value.Code, Name: value.Name}, nil
}

func coordinatorAssignmentContract(value eventsvc.CoordinatorAssignment) (contracts.CoordinatorAssignment, error) {
	id, err := uuid.Parse(value.ID)
	if err != nil {
		return contracts.CoordinatorAssignment{}, fmt.Errorf("event: map coordinator assignment id: %w", err)
	}
	userID, err := uuid.Parse(value.UserID)
	if err != nil {
		return contracts.CoordinatorAssignment{}, fmt.Errorf("event: map coordinator user id: %w", err)
	}
	eventID, err := uuid.Parse(value.EventID)
	if err != nil {
		return contracts.CoordinatorAssignment{}, fmt.Errorf("event: map coordinator event id: %w", err)
	}
	categories := make([]contracts.Category, 0, len(value.Categories))
	for _, category := range value.Categories {
		mapped, mapErr := categoryContract(category)
		if mapErr != nil {
			return contracts.CoordinatorAssignment{}, mapErr
		}
		categories = append(categories, mapped)
	}
	return contracts.CoordinatorAssignment{
		Id:         id,
		UserId:     userID,
		Email:      value.Email,
		Name:       value.Name,
		EventId:    eventID,
		Categories: categories,
		CreatedAt:  value.CreatedAt,
	}, nil
}

func captureCreditSummaryContract(value eventsvc.CaptureCreditSummary) (contracts.CaptureCreditSummary, error) {
	data := make([]contracts.CaptureCreditUsage, 0, len(value.Data))
	for _, row := range value.Data {
		mapped, mapErr := captureCreditUsageContract(row)
		if mapErr != nil {
			return contracts.CaptureCreditSummary{}, mapErr
		}
		data = append(data, mapped)
	}
	return contracts.CaptureCreditSummary{Data: data}, nil
}

func captureCreditUsageContract(value eventsvc.CaptureCreditUsage) (contracts.CaptureCreditUsage, error) {
	eventID, err := uuid.Parse(value.EventID)
	if err != nil {
		return contracts.CaptureCreditUsage{}, fmt.Errorf("event: map credit usage event id: %w", err)
	}
	userID, err := uuid.Parse(value.UserID)
	if err != nil {
		return contracts.CaptureCreditUsage{}, fmt.Errorf("event: map credit usage user id: %w", err)
	}
	return contracts.CaptureCreditUsage{
		EventId:   eventID,
		UserId:    userID,
		Email:     value.Email,
		Name:      value.Name,
		Limit:     value.Limit,
		Consumed:  value.Consumed,
		Remaining: value.Remaining,
		Exhausted: value.Exhausted,
	}, nil
}

func requestValidation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code: contracts.ErrorCodeVALIDATIONFAILED, Message: "Event validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func coordinatorRequestValidation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code: contracts.ErrorCodeVALIDATIONFAILED, Message: "Coordinator assignment validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func fail(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}
