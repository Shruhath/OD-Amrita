// Package attendanceimport exposes the admin bulk attendance import endpoint.
package attendanceimport

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	importsvc "github.com/Shruhath/OD-Amrita/api/internal/service/attendanceimport"
)

const maxUploadBytes int64 = importsvc.MaxUploadBytes

type Service interface {
	Create(context.Context, importsvc.Actor, importsvc.CreateInput) (importsvc.Import, error)
	Get(context.Context, importsvc.Actor, string, string) (importsvc.Import, error)
	ListErrors(context.Context, importsvc.Actor, string, string) ([]importsvc.RowError, error)
}

func RegisterAdminRoutes(group gin.IRouter, svc Service) {
	group.POST("/events/:event_id/attendance/imports", CreateHandler(svc))
	group.GET("/events/:event_id/attendance/imports/:import_id", GetHandler(svc))
	group.GET("/events/:event_id/attendance/imports/:import_id/errors", ErrorsHandler(svc))
}

func ErrorsHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := rbac.PrincipalFromContext(c)
		if !ok {
			c.Error(errors.New("attendance import: principal missing from context"))
			c.Abort()
			return
		}
		errorsOut, err := svc.ListErrors(c.Request.Context(), importsvc.Actor{UserID: principal.UserID, Email: principal.Email, Role: principal.Role}, c.Param("event_id"), c.Param("import_id"))
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		c.JSON(http.StatusOK, struct {
			Errors []importsvc.RowError `json:"errors"`
		}{Errors: errorsOut})
	}
}

func GetHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := rbac.PrincipalFromContext(c)
		if !ok {
			c.Error(errors.New("attendance import: principal missing from context"))
			c.Abort()
			return
		}
		result, err := svc.Get(c.Request.Context(), importsvc.Actor{UserID: principal.UserID, Email: principal.Email, Role: principal.Role}, c.Param("event_id"), c.Param("import_id"))
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func CreateHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)
		fileHeader, err := c.FormFile("file")
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		file, err := fileHeader.Open()
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		defer file.Close()
		content, err := io.ReadAll(http.MaxBytesReader(c.Writer, file, maxUploadBytes))
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		principal, ok := rbac.PrincipalFromContext(c)
		if !ok {
			c.Error(errors.New("attendance import: principal missing from context"))
			c.Abort()
			return
		}
		result, err := svc.Create(c.Request.Context(), importsvc.Actor{UserID: principal.UserID, Email: principal.Email, Role: principal.Role}, importsvc.CreateInput{
			EventID: c.Param("event_id"), Filename: fileHeader.Filename, CSV: content,
		})
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		c.JSON(http.StatusAccepted, struct {
			ID     uuid.UUID `json:"id"`
			Status string    `json:"status"`
		}{ID: result.ID, Status: result.Status})
	}
}
