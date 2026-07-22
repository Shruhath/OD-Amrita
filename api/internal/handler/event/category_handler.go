package event

// category_handler.go — the transport half of the category surface. Its only
// job is faithful translation: bind the path and body, hand server-owned actor
// context to the service, render the generated types, and push every failure
// through errormw so the wire always carries the centralized envelope.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	eventsvc "github.com/Shruhath/OD-Amrita/api/internal/service/event"
)

func ListCategoriesHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		categories, err := svc.ListCategories(c.Request.Context(), c.Param("event_id"))
		if err != nil {
			fail(c, err)
			return
		}
		// Preallocated to length zero so an event with no categories serializes
		// as an empty ARRAY, never null — a null breaks every consumer that maps
		// over the collection without a guard.
		data := make([]contracts.Category, 0, len(categories))
		for _, item := range categories {
			mapped, mapErr := categoryContract(item)
			if mapErr != nil {
				fail(c, mapErr)
				return
			}
			data = append(data, mapped)
		}
		c.JSON(http.StatusOK, contracts.CategoryCollection{Data: data})
	}
}

func CreateCategoryHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body contracts.CreateCategoryRequest
		if err := bindCreateCategoryRequest(c, &body); err != nil {
			fail(c, err)
			return
		}
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		// The event id comes from the PATH, which is the segment the route's
		// authorization was checked against. Honouring a body field here would
		// let a caller write into an event other than the one they asked for.
		created, err := svc.CreateCategory(c.Request.Context(), actor, eventsvc.CreateCategoryInput{
			EventID: c.Param("event_id"),
			Name:    body.Name,
		})
		if err != nil {
			fail(c, err)
			return
		}
		response, err := categoryContract(created)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusCreated, response)
	}
}

// createCategoryRequestShape keeps `name` raw for one pass so a missing field, a
// non-string field and malformed JSON stay distinguishable. Binding straight
// into the generated type collapses all three into one undifferentiated JSON
// error, and the portal needs the field to attach the message to the right input.
type createCategoryRequestShape struct {
	Name *json.RawMessage `json:"name"`
}

func bindCreateCategoryRequest(c *gin.Context, body *contracts.CreateCategoryRequest) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCreateRequestBytes)
	var shape createCategoryRequestShape
	if err := c.ShouldBindBodyWith(&shape, binding.JSON); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return requestValidation("$", "maximum", "Request body must not exceed 1 MiB.")
		}
		return requestValidation("$", "invalid_json", "Request body must be valid JSON.")
	}
	if shape.Name == nil || len(*shape.Name) == 0 {
		return requestValidation("name", "required", "Category name is required.")
	}
	var name string
	if err := json.Unmarshal(*shape.Name, &name); err != nil {
		return requestValidation("name", "invalid_format", "Category name must be a string.")
	}
	if err := c.ShouldBindBodyWith(body, binding.JSON); err != nil {
		return requestValidation("$", "invalid_json", "Request body must be valid JSON.")
	}
	return nil
}

func categoryContract(value eventsvc.Category) (contracts.Category, error) {
	id, err := uuid.Parse(value.ID)
	if err != nil {
		return contracts.Category{}, err
	}
	eventID, err := uuid.Parse(value.EventID)
	if err != nil {
		return contracts.Category{}, err
	}
	return contracts.Category{
		Id:        id,
		EventId:   eventID,
		Name:      value.Name,
		CreatedAt: value.CreatedAt,
	}, nil
}
