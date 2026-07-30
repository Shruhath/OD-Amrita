package odrecord

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	odsvc "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
)

type Service interface {
	Mark(context.Context, odsvc.Actor, odsvc.MarkInput) (odsvc.MarkResult, error)
}

func RegisterRoutes(group gin.IRouter, svc Service) {
	group.POST("/mark", MarkHandler(svc))
}

func MarkHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		body, err := bindMarkRequest(c, actor)
		if err != nil {
			fail(c, err)
			return
		}
		result, err := svc.Mark(c.Request.Context(), actor, serviceInput(body))
		if err != nil {
			fail(c, err)
			return
		}
		response, err := resultContract(result)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusCreated, response)
	}
}

const maxMarkRequestBytes int64 = 1 << 20

type markRequestShape struct {
	EventID       *json.RawMessage `json:"event_id"`
	CategoryID    *json.RawMessage `json:"category_id"`
	CaptureMethod *json.RawMessage `json:"capture_method"`
	StudentID     *json.RawMessage `json:"student_id"`
	Card          *json.RawMessage `json:"card"`
	Slot          *json.RawMessage `json:"slot"`
}

func bindMarkRequest(c *gin.Context, actor odsvc.Actor) (contracts.MarkAttendanceRequest, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxMarkRequestBytes)
	var shape markRequestShape
	if err := c.ShouldBindBodyWith(&shape, binding.JSON); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return contracts.MarkAttendanceRequest{}, requestValidation("$", "maximum", "Request body must not exceed 1 MiB.")
		}
		return contracts.MarkAttendanceRequest{}, requestValidation("$", "invalid_json", "Request body must be valid JSON.")
	}
	for field, raw := range map[string]*json.RawMessage{
		"event_id":       shape.EventID,
		"category_id":    shape.CategoryID,
		"capture_method": shape.CaptureMethod,
	} {
		if raw == nil || len(*raw) == 0 {
			return contracts.MarkAttendanceRequest{}, requestValidation(field, "required", field+" is required.")
		}
	}
	hasStudent := shape.StudentID != nil
	hasCard := shape.Card != nil
	if hasStudent == hasCard {
		return contracts.MarkAttendanceRequest{}, requestValidation("identity", "exactly_one", "Provide exactly one of student_id or card.")
	}
	if actor.Role == "coordinator" && shape.Slot != nil {
		return contracts.MarkAttendanceRequest{}, requestValidation("slot", "forbidden", "coordinator marks use server-derived slot only.")
	}

	var body contracts.MarkAttendanceRequest
	if err := c.ShouldBindBodyWith(&body, binding.JSON); err != nil {
		return contracts.MarkAttendanceRequest{}, requestValidation("$", "invalid_json", "Request body does not match the mark schema.")
	}
	if !body.CaptureMethod.Valid() {
		return contracts.MarkAttendanceRequest{}, requestValidation("capture_method", "unsupported", "capture_method must be one of nfc, camera, or search.")
	}
	return body, nil
}

func actorFromContext(c *gin.Context) (odsvc.Actor, error) {
	principal, ok := rbac.PrincipalFromContext(c)
	if !ok {
		return odsvc.Actor{}, domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Authenticated principal is required."}
	}
	return odsvc.Actor{UserID: principal.UserID, Email: principal.Email, Role: principal.Role}, nil
}

func serviceInput(body contracts.MarkAttendanceRequest) odsvc.MarkInput {
	input := odsvc.MarkInput{
		EventID:       body.EventId.String(),
		CategoryID:    body.CategoryId.String(),
		CaptureMethod: odsvc.CaptureMethod(body.CaptureMethod),
		Slot:          body.Slot,
	}
	if body.StudentId != nil {
		studentID := body.StudentId.String()
		input.StudentID = &studentID
	}
	if body.Card != nil {
		input.Card = &odsvc.CardInput{Version: body.Card.Version, Token: body.Card.Token}
	}
	return input
}

func resultContract(result odsvc.MarkResult) (contracts.MarkAttendanceResult, error) {
	recordID, err := uuid.Parse(result.RecordID)
	if err != nil {
		return contracts.MarkAttendanceResult{}, err
	}
	eventID, err := uuid.Parse(result.EventID)
	if err != nil {
		return contracts.MarkAttendanceResult{}, err
	}
	categoryID, err := uuid.Parse(result.CategoryID)
	if err != nil {
		return contracts.MarkAttendanceResult{}, err
	}
	studentID, err := uuid.Parse(result.Student.StudentID)
	if err != nil {
		return contracts.MarkAttendanceResult{}, err
	}
	return contracts.MarkAttendanceResult{
		RecordId:         recordID,
		EventId:          eventID,
		CategoryId:       categoryID,
		Slot:             result.Slot,
		CaptureMethod:    contracts.CaptureMethod(result.CaptureMethod),
		ServerReceivedAt: result.ServerReceivedAt,
		Student: contracts.MarkedStudent{
			StudentId:   studentID,
			RollNo:      result.Student.RollNo,
			DisplayName: result.Student.DisplayName,
			Branch:      result.Student.Branch,
			Section:     result.Student.Section,
			Batch:       result.Student.Batch,
		},
	}, nil
}

func requestValidation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Mark request validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func fail(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}
