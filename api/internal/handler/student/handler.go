package student

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	studentsvc "github.com/Shruhath/OD-Amrita/api/internal/service/student"
)

const maxRosterUploadBytes int64 = 2 << 20

type Service interface {
	Import(context.Context, studentsvc.Actor, studentsvc.ImportInput) (studentsvc.Result, error)
}

func RegisterAdminRoutes(group gin.IRouter, svc Service) {
	group.POST("/students/import", ImportStudentsHandler(svc))
}

func ImportStudentsHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRosterUploadBytes)
		fileHeader, err := c.FormFile("file")
		if err != nil {
			fail(c, multipartValidation(err))
			return
		}
		file, err := fileHeader.Open()
		if err != nil {
			fail(c, err)
			return
		}
		defer file.Close()

		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		result, err := svc.Import(c.Request.Context(), actor, studentsvc.ImportInput{
			FileName: fileHeader.Filename,
			Content:  file,
		})
		if err != nil {
			fail(c, err)
			return
		}
		response, err := importContract(result)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusCreated, response)
	}
}

func actorFromContext(c *gin.Context) (studentsvc.Actor, error) {
	principal, ok := rbac.PrincipalFromContext(c)
	if !ok {
		return studentsvc.Actor{}, errors.New("student import: principal missing from context")
	}
	return studentsvc.Actor{UserID: principal.UserID, Email: principal.Email, Role: principal.Role}, nil
}

func multipartValidation(err error) domain.DomainError {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) || strings.Contains(err.Error(), "request body too large") {
		return requestValidation("file", "maximum", "CSV upload must not exceed 2 MiB.")
	}
	return requestValidation("file", "required", "Student roster CSV is required.")
}

func importContract(result studentsvc.Result) (contracts.StudentRosterImportResult, error) {
	importID, err := uuid.Parse(result.ImportID)
	if err != nil {
		return contracts.StudentRosterImportResult{}, fmt.Errorf("student import: invalid import id: %w", err)
	}
	errorsOut := make([]contracts.StudentRosterImportError, 0, len(result.Errors))
	for _, item := range result.Errors {
		errorsOut = append(errorsOut, contracts.StudentRosterImportError{
			Row:     item.Row,
			Field:   item.Field,
			Code:    item.Code,
			Message: item.Message,
		})
	}
	return contracts.StudentRosterImportResult{
		ImportId:      importID,
		TotalRows:     result.TotalRows,
		InsertedRows:  result.InsertedRows,
		UpdatedRows:   result.UpdatedRows,
		UnchangedRows: result.UnchangedRows,
		InvalidRows:   result.InvalidRows,
		DatabaseCount: result.DatabaseCount,
		Errors:        errorsOut,
	}, nil
}

func requestValidation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Student roster validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func fail(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}
