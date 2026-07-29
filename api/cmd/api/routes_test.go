package main

// routes_test.go — the composition guard.
//
// The handler package tests build their own gin group; the rbac package tests
// mount their own stub handlers. Both pass whether or not PRODUCTION puts the
// admin routes behind RequireAdmin, because neither one exercises the wiring in
// run(). This file closes that gap: it composes the admin group exactly the way
// run() does and proves no admin route can be reached without passing the guard.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	handlercardtoken "github.com/Shruhath/OD-Amrita/api/internal/handler/cardtoken"
	handlerevent "github.com/Shruhath/OD-Amrita/api/internal/handler/event"
	handlerstudent "github.com/Shruhath/OD-Amrita/api/internal/handler/student"
	handlerteacher "github.com/Shruhath/OD-Amrita/api/internal/handler/teacher"
	errormw "github.com/Shruhath/OD-Amrita/api/internal/middleware/error"
	"github.com/Shruhath/OD-Amrita/api/internal/middleware/rbac"
	cardtokensvc "github.com/Shruhath/OD-Amrita/api/internal/service/cardtoken"
	eventsvc "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	studentsvc "github.com/Shruhath/OD-Amrita/api/internal/service/student"
	teachersvc "github.com/Shruhath/OD-Amrita/api/internal/service/teacher"
)

// refusingLoader stands in for the database-backed principal loader. Every admin
// request in this file is unauthenticated, so the loader must never be consulted
// at all — RequireAdmin rejects before it gets that far.
type refusingLoader struct{ calls int }

func (l *refusingLoader) Load(context.Context, string) (rbac.Principal, error) {
	l.calls++
	return rbac.Principal{}, errors.New("principal loader must not be reached for an unauthenticated request")
}

// reachCounter records whether a request made it past the middleware chain.
type reachCounter struct{ hits int }

func (r *reachCounter) handler(c *gin.Context) {
	r.hits++
	c.Status(http.StatusOK)
}

// reachingService is a complete, harmless implementation of the event handler's
// Service. It exists so that a route which slips past the guard fails as a clear
// assertion ("reached its handler without credentials") instead of a nil-pointer
// panic — the difference between a test that explains the defect and one that
// merely crashes near it.
type reachingService struct{ reached *reachCounter }

func (s reachingService) Branches(context.Context) ([]eventsvc.Branch, error) {
	s.reached.hits++
	return nil, nil
}

func (s reachingService) List(context.Context, eventsvc.ListInput) (eventsvc.Page, error) {
	s.reached.hits++
	return eventsvc.Page{}, nil
}

func (s reachingService) Get(context.Context, eventsvc.Actor, string) (eventsvc.Event, error) {
	s.reached.hits++
	return eventsvc.Event{}, nil
}

func (s reachingService) Create(context.Context, eventsvc.Actor, eventsvc.CreateInput) (eventsvc.Event, error) {
	s.reached.hits++
	return eventsvc.Event{}, nil
}

func (s reachingService) Archive(context.Context, eventsvc.Actor, string) (eventsvc.Event, error) {
	s.reached.hits++
	return eventsvc.Event{}, nil
}

func (s reachingService) ListCategories(context.Context, eventsvc.Actor, string) ([]eventsvc.Category, error) {
	s.reached.hits++
	return nil, nil
}

func (s reachingService) CreateCategory(context.Context, eventsvc.Actor, eventsvc.CreateCategoryInput) (eventsvc.Category, error) {
	s.reached.hits++
	return eventsvc.Category{}, nil
}

type reachingTeacherService struct{ reached *reachCounter }

func (s reachingTeacherService) List(context.Context, teachersvc.Actor, teachersvc.ListInput) (teachersvc.Page, error) {
	s.reached.hits++
	return teachersvc.Page{}, nil
}

func (s reachingTeacherService) Create(context.Context, teachersvc.Actor, teachersvc.CreateInput) (teachersvc.TeacherProfile, error) {
	s.reached.hits++
	return teachersvc.TeacherProfile{}, nil
}

type reachingStudentService struct{ reached *reachCounter }

func (s reachingStudentService) Import(context.Context, studentsvc.Actor, studentsvc.ImportInput) (studentsvc.Result, error) {
	s.reached.hits++
	return studentsvc.Result{}, nil
}

type reachingCardTokenService struct{ reached *reachCounter }

func (s reachingCardTokenService) Seal(context.Context, cardtokensvc.Actor, cardtokensvc.SealInput) (cardtokensvc.SealResult, error) {
	s.reached.hits++
	return cardtokensvc.SealResult{}, nil
}

func (s reachingCardTokenService) Resolve(context.Context, cardtokensvc.Actor, cardtokensvc.ResolveInput) (cardtokensvc.ResolvedStudent, error) {
	s.reached.hits++
	return cardtokensvc.ResolvedStudent{}, nil
}

func TestAdminGroupGuardsEveryRouteRegisterAdminRoutesMounts(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reached := &reachCounter{}
	loader := &refusingLoader{}

	router := gin.New()
	// The error middleware is part of the chain in run(), and it is what turns an
	// aborted request into a status code. Without it a rejected request still
	// carries gin's default 200, which would make the assertion below meaningless.
	router.Use(errormw.Handler())
	api := router.Group("/api/v1")
	admin := newAdminGroup(api, loader)
	// The same call run() makes, with a service-free stand-in: this test is about
	// the CHAIN in front of the handlers, not the handlers themselves.
	handlerevent.RegisterAdminRoutes(admin, reachingService{reached: reached})
	handlerteacher.RegisterAdminRoutes(admin, reachingTeacherService{reached: reached})
	handlerstudent.RegisterAdminRoutes(admin, reachingStudentService{reached: reached})
	handlercardtoken.RegisterAdminRoutes(admin, reachingCardTokenService{reached: reached})
	admin.POST("/users/:user_id/sessions/revoke", reached.handler)

	// Every route the admin group actually serves, discovered from the router
	// rather than restated here — a route added to RegisterAdminRoutes is covered
	// by this test the moment it is added.
	var adminRoutes []gin.RouteInfo
	for _, route := range router.Routes() {
		if len(route.Path) >= len("/api/v1/admin") && route.Path[:len("/api/v1/admin")] == "/api/v1/admin" {
			adminRoutes = append(adminRoutes, route)
		}
	}
	if len(adminRoutes) == 0 {
		t.Fatal("[P0] no /api/v1/admin routes were registered — the guard test is vacuous")
	}

	for _, route := range adminRoutes {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			before := reached.hits

			request := httptest.NewRequest(route.Method, concreteURL(route.Path), nil)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code == http.StatusOK {
				t.Errorf("[P0] %s %s answered 200 without credentials — the RBAC guard is not in this route's chain",
					route.Method, route.Path)
			}
			if reached.hits != before {
				t.Errorf("[P0] %s %s reached its handler without credentials", route.Method, route.Path)
			}
		})
	}

	if loader.calls != 0 {
		t.Errorf("principal loader was consulted %d times for unauthenticated requests; "+
			"RequireAdmin must reject before loading a principal", loader.calls)
	}
}

// concreteURL replaces gin's :param segments with a syntactically valid value so
// the request routes to the real handler chain.
func concreteURL(path string) string {
	const placeholder = "00000000-0000-4000-8000-000000000000"
	out := ""
	for _, segment := range splitPath(path) {
		if segment == "" {
			continue
		}
		if segment[0] == ':' || segment[0] == '*' {
			out += "/" + placeholder
			continue
		}
		out += "/" + segment
	}
	if out == "" {
		return "/"
	}
	return out
}

func splitPath(path string) []string {
	var parts []string
	current := ""
	for _, r := range path {
		if r == '/' {
			parts = append(parts, current)
			current = ""
			continue
		}
		current += string(r)
	}
	return append(parts, current)
}
