// Package teacher is the transport half of the teacher-profile surface. Its
// only job is faithful translation: bind the query/body, hand server-owned
// actor context to the service, render the generated types, and push every
// failure through errormw so the wire always carries the centralized
// envelope. Mirrors handler/event's shape and helpers.
package teacher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	teachersvc "github.com/Shruhath/OD-Amrita/api/internal/service/teacher"
)

type Service interface {
	List(context.Context, teachersvc.Actor, teachersvc.ListInput) (teachersvc.Page, error)
	Create(context.Context, teachersvc.Actor, teachersvc.CreateInput) (teachersvc.TeacherProfile, error)
}

// RegisterAdminRoutes mounts the teacher routes on an already-guarded group.
// It is hand-wired twice (cmd/api/main.go and api/test/portal/main.go); one
// registration function is what keeps them from drifting apart — the same
// reasoning handler/event's RegisterAdminRoutes documents.
func RegisterAdminRoutes(group gin.IRouter, svc Service) {
	group.GET("/teachers", ListTeachersHandler(svc))
	group.POST("/teachers", CreateTeacherHandler(svc))
}

func ListTeachersHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var params contracts.ListAdminTeachersParams
		if err := c.ShouldBindQuery(&params); err != nil {
			fail(c, requestValidation("$query", "invalid_format", "Query parameters are invalid."))
			return
		}
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		input := teachersvc.ListInput{}
		if params.Page != nil {
			input.Page = *params.Page
		}
		if params.PageSize != nil {
			input.PageSize = *params.PageSize
		}
		page, err := svc.List(c.Request.Context(), actor, input)
		if err != nil {
			fail(c, err)
			return
		}
		data := make([]contracts.TeacherProfile, 0, len(page.Data))
		for _, item := range page.Data {
			mapped, mapErr := teacherContract(item)
			if mapErr != nil {
				fail(c, mapErr)
				return
			}
			data = append(data, mapped)
		}
		c.JSON(http.StatusOK, contracts.TeacherProfileCollection{
			Data: data, Page: page.Page, PageSize: page.PageSize, Total: int(page.Total),
		})
	}
}

func CreateTeacherHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body contracts.CreateTeacherProfileRequest
		if err := bindCreateTeacherRequest(c, &body); err != nil {
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
		categoryIDs := make([]string, len(body.CategoryIds))
		for index, id := range body.CategoryIds {
			categoryIDs[index] = id.String()
		}
		created, err := svc.Create(c.Request.Context(), actor, teachersvc.CreateInput{
			Email: body.Email, BranchIDs: branchIDs, CategoryIDs: categoryIDs,
		})
		if err != nil {
			fail(c, err)
			return
		}
		response, err := teacherContract(created)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusCreated, response)
	}
}

// createTeacherRequestShape keeps email/branch_ids raw for one pass, the same
// call bindCreateRequest (handler/event) and bindCreateCategoryRequest
// (handler/event) make: a missing field, a non-string/non-array field and
// malformed JSON stay distinguishable, and the portal needs the field to
// attach the message to the right input.
type createTeacherRequestShape struct {
	Email       *json.RawMessage  `json:"email"`
	BranchIDs   []json.RawMessage `json:"branch_ids"`
	CategoryIDs []json.RawMessage `json:"category_ids"`
}

const maxCreateRequestBytes int64 = 1 << 20

func bindCreateTeacherRequest(c *gin.Context, body *contracts.CreateTeacherProfileRequest) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCreateRequestBytes)
	var shape createTeacherRequestShape
	if err := c.ShouldBindBodyWith(&shape, binding.JSON); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return requestValidation("$", "maximum", "Request body must not exceed 1 MiB.")
		}
		return requestValidation("$", "invalid_json", "Request body must be valid JSON.")
	}
	if shape.Email == nil || len(*shape.Email) == 0 {
		return requestValidation("email", "required", "Email is required.")
	}
	var email string
	if err := json.Unmarshal(*shape.Email, &email); err != nil {
		return requestValidation("email", "invalid_format", "Email must be a string.")
	}
	if len(shape.BranchIDs) == 0 {
		return requestValidation("branch_ids", "required", "Select at least one branch.")
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
	if len(shape.CategoryIDs) == 0 {
		return requestValidation("category_ids", "required", "Select at least one category.")
	}
	for index, raw := range shape.CategoryIDs {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return requestValidation(fmt.Sprintf("category_ids[%d]", index), "invalid_format", "Category id must be a UUID.")
		}
		if _, err := uuid.Parse(value); err != nil {
			return requestValidation(fmt.Sprintf("category_ids[%d]", index), "invalid_format", "Category id must be a UUID.")
		}
	}
	if err := c.ShouldBindBodyWith(body, binding.JSON); err != nil {
		return requestValidation("$", "invalid_json", "Request body must be valid JSON.")
	}
	return nil
}

func actorFromContext(c *gin.Context) (teachersvc.Actor, error) {
	principal, ok := rbac.PrincipalFromContext(c)
	if !ok {
		return teachersvc.Actor{}, errors.New("teacher: principal missing from context")
	}
	return teachersvc.Actor{UserID: principal.UserID, Email: principal.Email, Role: principal.Role}, nil
}

func teacherContract(value teachersvc.TeacherProfile) (contracts.TeacherProfile, error) {
	id, err := uuid.Parse(value.UserID)
	if err != nil {
		return contracts.TeacherProfile{}, err
	}
	branches := make([]contracts.BranchSummary, 0, len(value.Branches))
	for _, branch := range value.Branches {
		branchID, branchErr := uuid.Parse(branch.ID)
		if branchErr != nil {
			return contracts.TeacherProfile{}, branchErr
		}
		branches = append(branches, contracts.BranchSummary{Id: branchID, Code: branch.Code, Name: branch.Name})
	}
	categories := make([]contracts.Category, 0, len(value.Categories))
	for _, category := range value.Categories {
		categoryID, categoryErr := uuid.Parse(category.ID)
		if categoryErr != nil {
			return contracts.TeacherProfile{}, categoryErr
		}
		eventID, eventErr := uuid.Parse(category.EventID)
		if eventErr != nil {
			return contracts.TeacherProfile{}, eventErr
		}
		categories = append(categories, contracts.Category{
			Id:                       categoryID,
			EventId:                  eventID,
			Name:                     category.Name,
			VisibleOnPublicDashboard: category.VisibleOnPublicDashboard,
			CreatedAt:                category.CreatedAt,
		})
	}
	return contracts.TeacherProfile{
		UserId:     id,
		Email:      value.Email,
		Name:       value.Name,
		Branches:   branches,
		Categories: categories,
		CreatedAt:  value.CreatedAt,
	}, nil
}

func requestValidation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code: contracts.ErrorCodeVALIDATIONFAILED, Message: "Teacher profile validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func fail(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}
