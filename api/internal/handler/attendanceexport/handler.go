// Package attendanceexport exposes the admin attendance report workflow.
package attendanceexport

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	exportsvc "github.com/Shruhath/OD-Amrita/api/internal/service/attendanceexport"
)

type Service interface {
	Create(context.Context, exportsvc.Actor, string) (exportsvc.Export, error)
	Get(context.Context, exportsvc.Actor, string, string) (exportsvc.Export, error)
	Download(context.Context, exportsvc.Actor, string, string) (string, error)
}

func RegisterAdminRoutes(group gin.IRouter, svc Service) {
	group.POST("/events/:event_id/attendance/exports", CreateHandler(svc))
	group.GET("/events/:event_id/attendance/exports/:export_id", GetHandler(svc))
	group.GET("/events/:event_id/attendance/exports/:export_id/download", DownloadHandler(svc))
}

func actor(c *gin.Context) (exportsvc.Actor, error) {
	principal, ok := rbac.PrincipalFromContext(c)
	if !ok {
		return exportsvc.Actor{}, errors.New("attendance export: principal missing from context")
	}
	return exportsvc.Actor{UserID: principal.UserID, Email: principal.Email, Role: principal.Role}, nil
}

func CreateHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		who, err := actor(c)
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		result, err := svc.Create(c.Request.Context(), who, c.Param("event_id"))
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		c.JSON(http.StatusAccepted, result)
	}
}

func GetHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		who, err := actor(c)
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		result, err := svc.Get(c.Request.Context(), who, c.Param("event_id"), c.Param("export_id"))
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func DownloadHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		who, err := actor(c)
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		output, err := svc.Download(c.Request.Context(), who, c.Param("event_id"), c.Param("export_id"))
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", `attachment; filename="attendance.csv"`)
		c.String(http.StatusOK, output)
	}
}
