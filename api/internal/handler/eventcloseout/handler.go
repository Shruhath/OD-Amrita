package eventcloseout

import (
	"context"
	"errors"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	closeoutsvc "github.com/Shruhath/OD-Amrita/api/internal/service/eventcloseout"
	"github.com/gin-gonic/gin"
	"net/http"
)

type Service interface {
	Create(context.Context, closeoutsvc.Actor, string) (closeoutsvc.Closeout, error)
	Get(context.Context, closeoutsvc.Actor, string, string) (closeoutsvc.Closeout, error)
	Download(context.Context, closeoutsvc.Actor, string, string) (string, error)
}

func RegisterAdminRoutes(group gin.IRouter, svc Service) {
	group.POST("/events/:event_id/closeout", CreateHandler(svc))
	group.GET("/events/:event_id/closeout/:closeout_id", GetHandler(svc))
	group.GET("/events/:event_id/closeout/:closeout_id/report", DownloadHandler(svc))
}
func actor(c *gin.Context) (closeoutsvc.Actor, error) {
	p, ok := rbac.PrincipalFromContext(c)
	if !ok {
		return closeoutsvc.Actor{}, errors.New("event closeout: principal missing from context")
	}
	return closeoutsvc.Actor{UserID: p.UserID, Email: p.Email, Role: p.Role}, nil
}
func CreateHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		a, err := actor(c)
		if err != nil {
			c.Error(err)
			return
		}
		out, err := svc.Create(c, a, c.Param("event_id"))
		if err != nil {
			c.Error(err)
			return
		}
		c.JSON(http.StatusAccepted, out)
	}
}
func GetHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		a, err := actor(c)
		if err != nil {
			c.Error(err)
			return
		}
		out, err := svc.Get(c, a, c.Param("event_id"), c.Param("closeout_id"))
		if err != nil {
			c.Error(err)
			return
		}
		c.JSON(http.StatusOK, out)
	}
}
func DownloadHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		a, err := actor(c)
		if err != nil {
			c.Error(err)
			return
		}
		out, err := svc.Download(c, a, c.Param("event_id"), c.Param("closeout_id"))
		if err != nil {
			c.Error(err)
			return
		}
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", `attachment; filename="event-closeout.csv"`)
		c.String(http.StatusOK, out)
	}
}
