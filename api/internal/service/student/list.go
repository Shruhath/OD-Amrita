package student

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Card-populator roster paging. The populator lists one section at a time and
// a section is far smaller than 100 rows, so the default page fills a section
// in a single request without the operator paging mid-write.
const (
	defaultListPageSize = 100
	maxListPageSize     = 100
	maxListPage         = 10000
)

// ListInput filters the admin roster read. Every filter is optional; an empty
// string means "unfiltered" and is passed through to the query's sentinel.
type ListInput struct {
	BranchID string
	Batch    string
	Section  string
	Page     int
	PageSize int
}

// Student is the roster row the card populator displays.
//
// It carries display fields ONLY. There is deliberately no Email and no card
// token/payload field: the populator never needs them, and a field that does
// not exist cannot be leaked by a later careless mapping. The bound is
// structural, not a runtime blank check — see TestStudentListItemExposesNoPrivateFields.
type Student struct {
	StudentID   string
	RollNo      string
	DisplayName string
	Branch      string
	Section     string
	Batch       string
}

// ListResult is one deterministic page of the roster.
type ListResult struct {
	Students []Student
	Page     int
	PageSize int
	Total    int
}

// List returns one page of students for card population.
//
// RBAC is checked before any input validation and before the reader is touched,
// matching the ordering Story 0.3's review established for the card-token
// handlers: a non-admin must not be able to probe field validation behaviour.
func (s *Service) List(ctx context.Context, actor Actor, input ListInput) (ListResult, error) {
	if actor.Role != "admin" {
		return ListResult{}, forbidden()
	}

	normalized, err := validateList(input)
	if err != nil {
		return ListResult{}, err
	}

	students, total, err := s.reader.ListStudents(ctx, normalized)
	if err != nil {
		return ListResult{}, err
	}
	if students == nil {
		students = []Student{}
	}
	return ListResult{
		Students: students,
		Page:     normalized.Page,
		PageSize: normalized.PageSize,
		Total:    total,
	}, nil
}

func validateList(input ListInput) (ListInput, error) {
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PageSize == 0 {
		input.PageSize = defaultListPageSize
	}
	if input.Page < 1 {
		return ListInput{}, validation("page", "minimum", "Page must be at least 1.")
	}
	if input.Page > maxListPage {
		return ListInput{}, validation("page", "maximum", "Page must not exceed 10000.")
	}
	if input.PageSize < 1 {
		return ListInput{}, validation("page_size", "minimum", "Page size must be at least 1.")
	}
	if input.PageSize > maxListPageSize {
		return ListInput{}, validation("page_size", "maximum", "Page size must not exceed 100.")
	}

	input.BranchID = strings.TrimSpace(input.BranchID)
	if input.BranchID != "" {
		parsed, err := uuid.Parse(input.BranchID)
		if err != nil {
			return ListInput{}, validation("branch_id", "invalid_format", "Branch id must be a UUID.")
		}
		// Re-emit the canonical form so a mixed-case or braced UUID cannot
		// produce a different cache key or query parameter than it filters on.
		input.BranchID = parsed.String()
	}

	batch, err := validateListFilter("batch", input.Batch)
	if err != nil {
		return ListInput{}, err
	}
	input.Batch = batch

	section, err := validateListFilter("section", input.Section)
	if err != nil {
		return ListInput{}, err
	}
	input.Section = section

	return input, nil
}

// validateListFilter trims a roster filter and bounds it the same way the
// import path and the students CHECK constraints bound the stored column, so a
// filter can never be a value the column is incapable of holding.
func validateListFilter(field, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if !validText(trimmed) {
		return "", validation(field, "invalid_encoding", strings.ReplaceAll(field, "_", " ")+" must be valid UTF-8 text without NUL bytes.")
	}
	if utf8.RuneCountInString(trimmed) > maxSectionRunes {
		return "", validation(field, "maximum", strings.ReplaceAll(field, "_", " ")+" must not exceed 32 characters.")
	}
	return trimmed, nil
}
