package cardtoken

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
	cardtokensvc "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
)

type Service interface {
	Seal(context.Context, cardtokensvc.Actor, cardtokensvc.SealInput) (cardtokensvc.SealResult, error)
	Resolve(context.Context, cardtokensvc.Actor, cardtokensvc.ResolveInput) (cardtokensvc.ResolvedStudent, error)
}

func RegisterAdminRoutes(group gin.IRouter, svc Service) {
	group.POST("/students/:student_id/card-seal", SealHandler(svc))
	group.POST("/card-tokens/resolve", ResolveHandler(svc))
}

func SealHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		studentID := c.Param("student_id")
		if _, err := uuid.Parse(studentID); err != nil {
			fail(c, requestValidation("student_id", "invalid_format", "Student id must be a UUID."))
			return
		}
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		if actor.Role != "admin" {
			fail(c, forbidden())
			return
		}
		sealed, err := svc.Seal(c.Request.Context(), actor, cardtokensvc.SealInput{StudentID: studentID})
		if err != nil {
			fail(c, err)
			return
		}
		response, err := sealContract(sealed)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusCreated, response)
	}
}

func ResolveHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body contracts.CardTokenResolveRequest
		if err := bindResolveRequest(c, &body); err != nil {
			fail(c, err)
			return
		}
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		if actor.Role != "admin" {
			fail(c, forbidden())
			return
		}
		resolved, err := svc.Resolve(c.Request.Context(), actor, cardtokensvc.ResolveInput{Version: body.Version, Token: body.Token})
		if err != nil {
			fail(c, err)
			return
		}
		response, err := studentContract(resolved)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

type resolveRequestShape struct {
	Version *json.RawMessage `json:"version"`
	Token   *json.RawMessage `json:"token"`
}

const maxResolveRequestBytes int64 = 1 << 20

func bindResolveRequest(c *gin.Context, body *contracts.CardTokenResolveRequest) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxResolveRequestBytes)
	var shape resolveRequestShape
	if err := c.ShouldBindBodyWith(&shape, binding.JSON); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return requestValidation("$", "maximum", "Request body must not exceed 1 MiB.")
		}
		return requestValidation("$", "invalid_json", "Request body must be valid JSON.")
	}
	for field, raw := range map[string]*json.RawMessage{"version": shape.Version, "token": shape.Token} {
		if raw == nil || len(*raw) == 0 {
			return requestValidation(field, "required", fmt.Sprintf("%s is required.", field))
		}
		var value string
		if err := json.Unmarshal(*raw, &value); err != nil {
			return requestValidation(field, "invalid_format", fmt.Sprintf("%s must be a string.", field))
		}
	}
	if err := c.ShouldBindBodyWith(body, binding.JSON); err != nil {
		return requestValidation("$", "invalid_json", "Request body must be valid JSON.")
	}
	return nil
}

func actorFromContext(c *gin.Context) (cardtokensvc.Actor, error) {
	principal, ok := rbac.PrincipalFromContext(c)
	if !ok {
		return cardtokensvc.Actor{}, errors.New("cardtoken: principal missing from context")
	}
	return cardtokensvc.Actor{UserID: principal.UserID, Email: principal.Email, Role: principal.Role}, nil
}

func sealContract(sealed cardtokensvc.SealResult) (contracts.CardSeal, error) {
	student, err := studentContract(sealed.Student)
	if err != nil {
		return contracts.CardSeal{}, err
	}
	response := contracts.CardSeal{
		Student: student,
		Version: sealed.Version,
		Token:   sealed.Token,
	}
	response.Payload.Version = sealed.Payload.Version
	response.Payload.Token = sealed.Payload.Token
	return response, nil
}

func studentContract(student cardtokensvc.ResolvedStudent) (contracts.ResolvedCardStudent, error) {
	studentID, err := uuid.Parse(student.StudentID)
	if err != nil {
		return contracts.ResolvedCardStudent{}, err
	}
	return contracts.ResolvedCardStudent{
		StudentId:   studentID,
		RollNo:      student.RollNo,
		DisplayName: student.DisplayName,
		Branch:      student.Branch,
		Section:     student.Section,
		Batch:       student.Batch,
	}, nil
}

func requestValidation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code: contracts.ErrorCodeVALIDATIONFAILED, Message: "Card token validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func forbidden() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Admin role required."}
}

func fail(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}
