package dispute

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	svcdispute "github.com/Shruhath/OD-Amrita/api/internal/service/dispute"
	"github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
)

type PublicService interface {
	Submit(context.Context, svcdispute.SubmitInput) (svcdispute.Receipt, error)
	PublicEvents(context.Context) (publicattendance.EventCollection, error)
}

type AuthenticatedService interface {
	List(context.Context, svcdispute.Actor, svcdispute.ListFilter) (svcdispute.Collection, error)
	Resolve(context.Context, svcdispute.Actor, string, string) (svcdispute.Record, error)
}

func RegisterPublicRoutes(group gin.IRouter, svc PublicService, writeLimiter gin.HandlerFunc) {
	group.GET("/events", PublicEventsHandler(svc))
	group.POST("/disputes", writeLimiter, SubmitPublicDisputeHandler(svc))
}

func RegisterRoutes(group gin.IRouter, svc AuthenticatedService) {
	group.GET("/disputes", ListDisputesHandler(svc))
	group.POST("/disputes/:dispute_id/resolution", ResolveDisputeHandler(svc))
}

func SubmitPublicDisputeHandler(svc PublicService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body contracts.PublicDisputeRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			fail(c, validation("$", "invalid_json", "Request body is invalid."))
			return
		}
		input := svcdispute.SubmitInput{
			StudentID:              body.StudentId.String(),
			SubmittedEmail:         body.SubmittedEmail,
			EventID:                body.EventId.String(),
			CategoryID:             body.CategoryId.String(),
			ODDate:                 body.OdDate.Time.Format(time.DateOnly),
			Slot:                   body.Slot,
			ClaimedCoordinatorName: body.ClaimedCoordinatorName,
			StudentNote:            body.StudentNote,
		}
		receipt, err := svc.Submit(c.Request.Context(), input)
		if err != nil {
			fail(c, err)
			return
		}
		rendered, err := receiptContract(receipt)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusCreated, rendered)
	}
}

func PublicEventsHandler(svc PublicService) gin.HandlerFunc {
	return func(c *gin.Context) {
		events, err := svc.PublicEvents(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		data := make([]contracts.PublicEvent, 0, len(events.Data))
		for _, event := range events.Data {
			eventID, err := uuid.Parse(event.EventID)
			if err != nil {
				fail(c, err)
				return
			}
			eventDate, err := parseDate(event.EventDate)
			if err != nil {
				fail(c, err)
				return
			}
			categories := make([]contracts.PublicEventCategory, 0, len(event.Categories))
			for _, category := range event.Categories {
				categoryID, err := uuid.Parse(category.ID)
				if err != nil {
					fail(c, err)
					return
				}
				categories = append(categories, contracts.PublicEventCategory{Id: categoryID, Name: category.Name})
			}
			data = append(data, contracts.PublicEvent{
				EventId:    eventID,
				Name:       event.Name,
				EventDate:  eventDate,
				Categories: categories,
			})
		}
		c.JSON(http.StatusOK, contracts.PublicEventCollection{Data: data})
	}
}

func ListDisputesHandler(svc AuthenticatedService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		filter, err := listFilter(c)
		if err != nil {
			fail(c, err)
			return
		}
		collection, err := svc.List(c.Request.Context(), actor, filter)
		if err != nil {
			fail(c, err)
			return
		}
		data := make([]contracts.Dispute, 0, len(collection.Data))
		for _, record := range collection.Data {
			rendered, err := disputeContract(record)
			if err != nil {
				fail(c, err)
				return
			}
			data = append(data, rendered)
		}
		c.JSON(http.StatusOK, contracts.DisputeCollection{
			Data: data, Page: collection.Page, PageSize: collection.PageSize, Total: collection.Total,
		})
	}
}

func ResolveDisputeHandler(svc AuthenticatedService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		disputeID := strings.TrimSpace(c.Param("dispute_id"))
		if _, err := uuid.Parse(disputeID); err != nil {
			fail(c, validation("dispute_id", "invalid_format", "dispute_id must be a UUID."))
			return
		}
		var body contracts.DisputeResolutionRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			fail(c, validation("$", "invalid_json", "Request body is invalid."))
			return
		}
		record, err := svc.Resolve(c.Request.Context(), actor, disputeID, body.ResolutionNote)
		if err != nil {
			fail(c, err)
			return
		}
		rendered, err := disputeContract(record)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, rendered)
	}
}

func actorFromContext(c *gin.Context) (svcdispute.Actor, error) {
	principal, ok := rbac.PrincipalFromContext(c)
	if !ok {
		return svcdispute.Actor{}, domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Authenticated principal access is required."}
	}
	return svcdispute.Actor{UserID: principal.UserID, Email: principal.Email, Role: principal.Role}, nil
}

func listFilter(c *gin.Context) (svcdispute.ListFilter, error) {
	page, err := optionalPositive(c.Query("page"), "page")
	if err != nil {
		return svcdispute.ListFilter{}, err
	}
	pageSize, err := optionalPositive(c.Query("page_size"), "page_size")
	if err != nil {
		return svcdispute.ListFilter{}, err
	}
	status := strings.TrimSpace(c.Query("status"))
	if status != "" && status != "open" && status != "resolved" && status != "all" {
		return svcdispute.ListFilter{}, validation("status", "invalid_value", "status must be open, resolved, or all.")
	}
	return svcdispute.ListFilter{Page: page, PageSize: pageSize, Status: status}, nil
}

func optionalPositive(raw, field string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, validation(field, "minimum", field+" must be a positive integer.")
	}
	return value, nil
}

func receiptContract(receipt svcdispute.Receipt) (contracts.PublicDisputeReceipt, error) {
	disputeID := uuid.Nil
	if strings.TrimSpace(receipt.DisputeID) != "" {
		parsed, err := uuid.Parse(receipt.DisputeID)
		if err != nil {
			return contracts.PublicDisputeReceipt{}, err
		}
		disputeID = parsed
	}
	student, err := publicStudent(receipt.Student)
	if err != nil {
		return contracts.PublicDisputeReceipt{}, err
	}
	return contracts.PublicDisputeReceipt{
		DisputeId:   disputeID,
		Reference:   receipt.Reference,
		Status:      contracts.PublicDisputeReceiptStatusOpen,
		SubmittedAt: receipt.SubmittedAt,
		Student:     student,
	}, nil
}

func disputeContract(record svcdispute.Record) (contracts.Dispute, error) {
	disputeID, err := uuid.Parse(record.DisputeID)
	if err != nil {
		return contracts.Dispute{}, err
	}
	eventID, err := uuid.Parse(record.EventID)
	if err != nil {
		return contracts.Dispute{}, err
	}
	categoryID, err := uuid.Parse(record.CategoryID)
	if err != nil {
		return contracts.Dispute{}, err
	}
	eventDate, err := parseDate(record.EventDate)
	if err != nil {
		return contracts.Dispute{}, err
	}
	odDate, err := parseDate(record.ODDate)
	if err != nil {
		return contracts.Dispute{}, err
	}
	student, err := publicStudent(record.Student)
	if err != nil {
		return contracts.Dispute{}, err
	}
	var resolvedBy *openapi_types.UUID
	if record.ResolvedByUserID != nil {
		parsed, err := uuid.Parse(*record.ResolvedByUserID)
		if err != nil {
			return contracts.Dispute{}, err
		}
		resolvedBy = &parsed
	}
	return contracts.Dispute{
		DisputeId:              disputeID,
		Reference:              record.Reference,
		Student:                student,
		SubmittedEmail:         record.SubmittedEmail,
		EventId:                eventID,
		EventName:              record.EventName,
		EventDate:              eventDate,
		CategoryId:             categoryID,
		CategoryName:           record.CategoryName,
		OdDate:                 odDate,
		Slot:                   record.Slot,
		ClaimedCoordinatorName: record.ClaimedCoordinatorName,
		StudentNote:            record.StudentNote,
		Status:                 contracts.DisputeStatus(record.Status),
		SubmittedAt:            record.SubmittedAt,
		ResolutionNote:         record.ResolutionNote,
		ResolvedByUserId:       resolvedBy,
		ResolvedAt:             record.ResolvedAt,
	}, nil
}

func publicStudent(student svcdispute.Student) (contracts.PublicStudent, error) {
	studentID := uuid.Nil
	if strings.TrimSpace(student.StudentID) != "" {
		parsed, err := uuid.Parse(student.StudentID)
		if err != nil {
			return contracts.PublicStudent{}, err
		}
		studentID = parsed
	}
	return contracts.PublicStudent{
		StudentId:   studentID,
		RollNo:      student.RollNo,
		DisplayName: student.DisplayName,
		Branch:      student.Branch,
		Section:     student.Section,
		Batch:       student.Batch,
	}, nil
}

func parseDate(value string) (openapi_types.Date, error) {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return openapi_types.Date{}, err
	}
	return openapi_types.Date{Time: parsed}, nil
}

func validation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Dispute request validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func fail(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}
