package odrecord

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
	svcstudent "github.com/Shruhath/OD-Amrita/api/internal/service/student"
)

// MarkingReadService is everything the coordinator marking surface reads.
//
// One dependency, deliberately: no handler here reaches a repository directly,
// so none can serve a scope the mark write would refuse. The scope-describing
// reads (events, categories, mark status) run the full policy; the roster search
// runs the role half only, for the reasons set out on marking.ReadService.
type MarkingReadService interface {
	VisibleEvents(context.Context, marking.Actor) ([]marking.Event, error)
	Categories(ctx context.Context, actor marking.Actor, eventID string) ([]marking.Category, error)
	SearchStudents(ctx context.Context, actor marking.Actor, query string, limit int) ([]marking.Student, error)
	MarkStatus(ctx context.Context, actor marking.Actor, eventID, categoryID string) (marking.Status, error)
}

// RegisterMarkingReadRoutes mounts the four reads on the AUTHENTICATED group.
//
// Never under /admin: a coordinator would get 403 on every read and the screen
// would be dead. Never without a principal either — `students` is 1,534 real
// people and the counters are live event data.
func RegisterMarkingReadRoutes(group gin.IRouter, svc MarkingReadService) {
	group.GET("/events", MarkableEventsHandler(svc))
	group.GET("/events/:event_id/categories", MarkableCategoriesHandler(svc))
	group.GET("/students/search", SearchStudentsHandler(svc))
	group.GET("/mark/status", MarkStatusHandler(svc))
}

const (
	defaultEventPage     = 1
	defaultEventPageSize = 20
	maxEventPageSize     = 100
	// maxEventPage is a real ceiling, not decoration. Without one, `page` was
	// unbounded while `page_size` was capped, and `(page-1)*pageSize` in
	// pageWindow overflowed int64 and wrapped NEGATIVE — `events[-16:1]` panics.
	// gin.Recovery turned that into a bare 500 that skipped the typed error
	// envelope and logged a stack trace per request.
	maxEventPage = 10_000
)

func MarkableEventsHandler(svc MarkingReadService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := markingActor(c)
		if err != nil {
			fail(c, err)
			return
		}
		// Validated BEFORE the read: a malformed page must not cost an event and
		// branch query first. This is the same rule the search handler states
		// below, and it runs after markingActor so authorize-before-validate is
		// preserved.
		page, pageSize, err := eventPagination(c)
		if err != nil {
			fail(c, err)
			return
		}
		events, err := svc.VisibleEvents(c.Request.Context(), actor)
		if err != nil {
			fail(c, err)
			return
		}

		total := len(events)
		slice := pageWindow(total, page, pageSize)
		data := make([]contracts.Event, 0, slice.to-slice.from)
		for _, event := range events[slice.from:slice.to] {
			rendered, convErr := eventContract(event)
			if convErr != nil {
				fail(c, convErr)
				return
			}
			data = append(data, rendered)
		}
		c.JSON(http.StatusOK, contracts.EventCollection{
			Data:     data,
			Page:     page,
			PageSize: pageSize,
			Total:    total,
		})
	}
}

func MarkableCategoriesHandler(svc MarkingReadService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := markingActor(c)
		if err != nil {
			fail(c, err)
			return
		}
		eventID := strings.TrimSpace(c.Param("event_id"))
		if _, parseErr := uuid.Parse(eventID); parseErr != nil {
			fail(c, readValidation("event_id", "invalid_format", "event_id must be a UUID."))
			return
		}
		categories, err := svc.Categories(c.Request.Context(), actor, eventID)
		if err != nil {
			fail(c, err)
			return
		}
		data := make([]contracts.Category, 0, len(categories))
		for _, category := range categories {
			rendered, convErr := categoryContract(category)
			if convErr != nil {
				fail(c, convErr)
				return
			}
			data = append(data, rendered)
		}
		c.JSON(http.StatusOK, contracts.CategoryCollection{Data: data})
	}
}

func SearchStudentsHandler(svc MarkingReadService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := searchActor(c)
		if err != nil {
			fail(c, err)
			return
		}
		limit, err := searchLimit(c)
		if err != nil {
			fail(c, err)
			return
		}
		// Rejected at the edge so a too-short query costs no service round trip.
		// The bound itself comes from the service, which checks it again — there is
		// one number, and a non-HTTP caller cannot skip it.
		query := strings.TrimSpace(c.Query("q"))
		if utf8.RuneCountInString(query) < svcstudent.MinSearchRunes {
			fail(c, readValidation("q", "min_length", "Search needs at least 3 characters."))
			return
		}
		if utf8.RuneCountInString(query) > svcstudent.MaxSearchRunes {
			fail(c, readValidation("q", "max_length", "Search must not exceed 64 characters."))
			return
		}

		results, err := svc.SearchStudents(c.Request.Context(), actor, query, limit)
		if err != nil {
			fail(c, err)
			return
		}
		data := make([]contracts.ResolvedCardStudent, 0, len(results))
		for _, student := range results {
			rendered, convErr := studentContract(student)
			if convErr != nil {
				fail(c, convErr)
				return
			}
			data = append(data, rendered)
		}
		c.JSON(http.StatusOK, contracts.StudentSearchResults{Data: data})
	}
}

func MarkStatusHandler(svc MarkingReadService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := markingActor(c)
		if err != nil {
			fail(c, err)
			return
		}
		eventID := strings.TrimSpace(c.Query("event_id"))
		categoryID := strings.TrimSpace(c.Query("category_id"))
		if details := missingScopeDetails(eventID, categoryID); len(details) > 0 {
			fail(c, readValidationDetails(details))
			return
		}

		status, err := svc.MarkStatus(c.Request.Context(), actor, eventID, categoryID)
		if err != nil {
			fail(c, err)
			return
		}
		body := contracts.MarkStatus{
			ServerTime: status.ServerTime,
			Counters: contracts.MarkCounters{
				Mine:          status.Counters.Mine,
				CategoryTotal: status.Counters.CategoryTotal,
			},
			FallbackCredit: contracts.FallbackCreditState{
				Limit:            status.FallbackCredit.Limit,
				Consumed:         status.FallbackCredit.Consumed,
				Remaining:        status.FallbackCredit.Remaining,
				Exhausted:        status.FallbackCredit.Exhausted,
				WarningThreshold: &status.FallbackCredit.WarningThreshold,
			},
		}
		if status.Slot != nil {
			body.Slot = &contracts.MarkSlotWindow{
				Slot:      status.Slot.Slot,
				StartedAt: status.Slot.StartedAt,
				EndsAt:    status.Slot.EndsAt,
			}
		}
		c.JSON(http.StatusOK, body)
	}
}

// markingActor loads the authenticated principal. It runs FIRST in every handler
// above: if a handler judged its parameters before authorizing, the difference
// between 400 and 403 would tell an unauthorized caller which parameters are
// well-formed — a free oracle over the parameter space (the defect Story 0.3's
// review fixed in the card-token handlers).
func markingActor(c *gin.Context) (marking.Actor, error) {
	actor, err := actorFromContext(c)
	if err != nil {
		return marking.Actor{}, err
	}
	if actor.Role != "coordinator" {
		return marking.Actor{}, domainForbidden()
	}
	return marking.Actor{UserID: actor.UserID, Email: actor.Email, Role: actor.Role}, nil
}

// searchActor is markingActor's widened sibling for SearchStudentsHandler
// alone. Story 0.9's teacher/admin search-mark screen calls this exact route
// before it can select anyone to mark, so coordinator-only here would 403 the
// entire teacher/admin surface at its first step. The other three reads
// (events, categories, mark status) stay behind markingActor — they describe
// the COORDINATOR's own scope, which teacher/admin never uses; they read
// their own events/categories through the separate portal-mark surface.
func searchActor(c *gin.Context) (marking.Actor, error) {
	actor, err := actorFromContext(c)
	if err != nil {
		return marking.Actor{}, err
	}
	switch actor.Role {
	case "coordinator", "teacher", "admin":
		return marking.Actor{UserID: actor.UserID, Email: actor.Email, Role: actor.Role}, nil
	default:
		return marking.Actor{}, domainForbidden()
	}
}

func missingScopeDetails(eventID, categoryID string) []contracts.ValidationErrorDetail {
	var details []contracts.ValidationErrorDetail
	for _, field := range []struct {
		name  string
		value string
	}{{"event_id", eventID}, {"category_id", categoryID}} {
		if field.value == "" {
			details = append(details, contracts.ValidationErrorDetail{
				Field:   field.name,
				Code:    "required",
				Message: field.name + " is required.",
			})
			continue
		}
		if _, err := uuid.Parse(field.value); err != nil {
			details = append(details, contracts.ValidationErrorDetail{
				Field:   field.name,
				Code:    "invalid_format",
				Message: field.name + " must be a UUID.",
			})
		}
	}
	return details
}

func eventPagination(c *gin.Context) (int, int, error) {
	page, err := boundedQueryInt(c, "page", defaultEventPage, 1, maxEventPage)
	if err != nil {
		return 0, 0, err
	}
	pageSize, err := boundedQueryInt(c, "page_size", defaultEventPageSize, 1, maxEventPageSize)
	if err != nil {
		return 0, 0, err
	}
	return page, pageSize, nil
}

func searchLimit(c *gin.Context) (int, error) {
	// 0 means "unspecified"; the service applies its own default and cap, so the
	// bound lives in one place rather than two that can drift apart. The ceiling
	// is the SERVICE's exported constant for exactly that reason — a literal here
	// was a second copy of the number that could be raised independently.
	raw := strings.TrimSpace(c.Query("limit"))
	if raw == "" {
		return 0, nil
	}
	return boundedQueryInt(c, "limit", 0, 1, svcstudent.MaxSearchLimit)
}

// boundedQueryInt rejects a non-numeric or out-of-range value rather than
// silently clamping it: a coordinator whose URL says page_size=500 should be
// told, not quietly served 100.
func boundedQueryInt(c *gin.Context, name string, fallback, min, max int) (int, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, readValidation(name, "invalid_format", name+" must be an integer.")
	}
	if value < min || (max > 0 && value > max) {
		return 0, readValidation(name, "range", name+" is outside the permitted range.")
	}
	return value, nil
}

// pageSlice bounds the requested page against the visible set.
type pageSlice struct{ from, to int }

func pageWindow(total, page, pageSize int) pageSlice {
	from := (page - 1) * pageSize
	// `from < 0` is the overflow guard, and it stays even though maxEventPage
	// now makes the multiplication safe: this function slices a real array, so
	// it must be total-ordering-safe on its own terms rather than trusting a
	// caller's bound that a later edit could widen.
	if from < 0 || from >= total {
		return pageSlice{from: total, to: total}
	}
	to := from + pageSize
	if to > total {
		to = total
	}
	return pageSlice{from: from, to: to}
}

func eventContract(event marking.Event) (contracts.Event, error) {
	id, err := uuid.Parse(event.EventID)
	if err != nil {
		return contracts.Event{}, err
	}
	branches := make([]contracts.BranchSummary, 0, len(event.Branches))
	for _, branch := range event.Branches {
		branchID, branchErr := uuid.Parse(branch.ID)
		if branchErr != nil {
			return contracts.Event{}, branchErr
		}
		branches = append(branches, contracts.BranchSummary{Id: branchID, Code: branch.Code, Name: branch.Name})
	}
	rendered := contracts.Event{
		Id:         id,
		Name:       event.Name,
		Status:     contracts.EventStatus(event.Status),
		Branches:   branches,
		CreatedAt:  event.CreatedAt,
		ArchivedAt: event.ArchivedAt,
	}
	rendered.EventDate.Time = event.EventDate
	rendered.EndDate.Time = event.EndDate
	return rendered, nil
}

func categoryContract(category marking.Category) (contracts.Category, error) {
	id, err := uuid.Parse(category.CategoryID)
	if err != nil {
		return contracts.Category{}, err
	}
	eventID, err := uuid.Parse(category.EventID)
	if err != nil {
		return contracts.Category{}, err
	}
	return contracts.Category{
		Id:        id,
		EventId:   eventID,
		Name:      category.Name,
		CreatedAt: category.CreatedAt,
	}, nil
}

func studentContract(student marking.Student) (contracts.ResolvedCardStudent, error) {
	id, err := uuid.Parse(student.StudentID)
	if err != nil {
		return contracts.ResolvedCardStudent{}, err
	}
	return contracts.ResolvedCardStudent{
		StudentId:   id,
		RollNo:      student.RollNo,
		DisplayName: student.DisplayName,
		Branch:      student.Branch,
		Section:     student.Section,
		Batch:       student.Batch,
	}, nil
}

func readValidation(field, code, message string) error {
	return readValidationDetails([]contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}})
}

func readValidationDetails(details []contracts.ValidationErrorDetail) error {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Marking read validation failed.",
		Details: details,
	}
}

func domainForbidden() error {
	return domain.DomainError{
		Code:    contracts.ErrorCodeRBACFORBIDDEN,
		Message: "Only a coordinator may use the marking surface.",
	}
}
