package student

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	studentsvc "github.com/Shruhath/OD-Amrita/api/internal/service/student"
)

// ListStudentsHandler serves the admin card-populator roster read.
//
// RBAC is asserted here as well as on the route group. The group guard is the
// real gate, but the handler check keeps the ordering explicit and survives a
// future remount — the same belt-and-braces the card-token handlers use.
func ListStudentsHandler(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, err := actorFromContext(c)
		if err != nil {
			fail(c, err)
			return
		}
		if actor.Role != "admin" {
			fail(c, forbidden())
			return
		}

		input, err := listInputFromQuery(c)
		if err != nil {
			fail(c, err)
			return
		}

		result, err := svc.List(c.Request.Context(), actor, input)
		if err != nil {
			fail(c, err)
			return
		}
		response, err := listContract(result)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

// listInputFromQuery parses the query string into service input.
//
// Parsing is deliberately strict rather than lenient: a page value of "abc"
// becomes a typed 400, not a silent fallback to page 1. During card population
// a silently-wrong page shows the wrong students, and the operator has no way
// to tell that from a correct empty section.
func listInputFromQuery(c *gin.Context) (studentsvc.ListInput, error) {
	// Filter LENGTH and canonicalisation stay in the service — one place for
	// those rules. Parsed into a Go type at this boundary are only the values
	// whose malformed form has no service-level representation: a non-integer
	// page, and a branch_id that is not a UUID. Both are re-checked by the
	// service, which stays authoritative for callers that bypass this handler.
	input := studentsvc.ListInput{
		BranchID: c.Query("branch_id"),
		Batch:    c.Query("batch"),
		Section:  c.Query("section"),
	}

	if raw := strings.TrimSpace(input.BranchID); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			return studentsvc.ListInput{}, requestValidation("branch_id", "invalid_format", "Branch id must be a UUID.")
		}
	}

	page, err := optionalPositiveInt(c, "page")
	if err != nil {
		return studentsvc.ListInput{}, err
	}
	input.Page = page

	pageSize, err := optionalPositiveInt(c, "page_size")
	if err != nil {
		return studentsvc.ListInput{}, err
	}
	input.PageSize = pageSize

	return input, nil
}

func optionalPositiveInt(c *gin.Context, field string) (int, error) {
	raw := strings.TrimSpace(c.Query(field))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, requestValidation(field, "invalid_format", strings.ReplaceAll(field, "_", " ")+" must be an integer.")
	}
	if value < 1 {
		return 0, requestValidation(field, "minimum", strings.ReplaceAll(field, "_", " ")+" must be at least 1.")
	}
	return value, nil
}

func listContract(result studentsvc.ListResult) (contracts.StudentCollection, error) {
	data := make([]contracts.ResolvedCardStudent, 0, len(result.Students))
	for _, item := range result.Students {
		studentID, err := uuid.Parse(item.StudentID)
		if err != nil {
			return contracts.StudentCollection{}, err
		}
		data = append(data, contracts.ResolvedCardStudent{
			StudentId:   studentID,
			RollNo:      item.RollNo,
			DisplayName: item.DisplayName,
			Branch:      item.Branch,
			Section:     item.Section,
			Batch:       item.Batch,
		})
	}
	return contracts.StudentCollection{
		Data:     data,
		Page:     result.Page,
		PageSize: result.PageSize,
		Total:    result.Total,
	}, nil
}

func forbidden() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Administrator access is required."}
}
