package student

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

var ErrBranchNotFound = errors.New("student: branch not found")

const (
	maxRollRunes     = 64
	maxNameRunes     = 200
	maxBranchRunes   = 32
	maxSectionRunes  = 32
	maxBatchRunes    = 32
	maxCSVRows       = 2000
	maxRowErrors     = 1000
	defaultSourceCSV = "students.csv"
)

var rollPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._/-]*$`)

type Actor struct {
	UserID string
	Email  string
	Role   string
}

type ImportInput struct {
	FileName string
	Content  io.Reader
}

type Branch struct {
	ID   string
	Code string
}

type ImportRow struct {
	RollNo   string
	Name     string
	BranchID string
	Section  string
	Batch    string
}

type RowError struct {
	Row     int
	Field   string
	Code    string
	Message string
}

type Result struct {
	ImportID      string
	TotalRows     int
	InsertedRows  int
	UpdatedRows   int
	UnchangedRows int
	InvalidRows   int
	DatabaseCount int
	Errors        []RowError
}

type ImportWrite struct {
	SourceFilename string
	Actor          Actor
	Rows           []ImportRow
	TotalRows      int
	InvalidRows    int
	Errors         []RowError
}

type Reader interface {
	ResolveBranch(context.Context, string) (Branch, error)
}

type Writer interface {
	Import(context.Context, pgx.Tx, ImportWrite) (Result, error)
}

type AuditStore interface {
	WithAudit(context.Context, store.AuditEvent, func(context.Context, pgx.Tx) (bool, error)) error
}

type Service struct {
	reader Reader
	store  AuditStore
	writer Writer
}

func NewService(reader Reader, audited AuditStore, writer Writer) *Service {
	return &Service{reader: reader, store: audited, writer: writer}
}

func (s *Service) Import(ctx context.Context, actor Actor, input ImportInput) (Result, error) {
	if actor.Role != "admin" {
		return Result{}, forbidden()
	}
	if input.Content == nil {
		return Result{}, validation("$", "required", "CSV file is required.")
	}
	source := strings.TrimSpace(input.FileName)
	if source == "" {
		source = defaultSourceCSV
	}
	if !validText(source) {
		return Result{}, validation("file", "invalid_encoding", "CSV filename must be valid UTF-8 text without NUL bytes.")
	}
	if utf8.RuneCountInString(source) > 255 {
		return Result{}, validation("file", "maximum", "CSV filename must not exceed 255 characters.")
	}

	totalRows, rows, invalidRows, rowErrors, err := s.parse(ctx, input.Content)
	if err != nil {
		return Result{}, err
	}
	if len(rows) == 0 {
		details := make([]contracts.ValidationErrorDetail, 0, max(1, len(rowErrors)))
		for _, rowErr := range rowErrors {
			details = append(details, rowErr.validationDetail())
		}
		if len(details) == 0 {
			details = append(details, contracts.ValidationErrorDetail{Field: "file", Code: "empty", Message: "CSV must contain at least one valid student row."})
		}
		return Result{}, domain.DomainError{Code: contracts.ErrorCodeVALIDATIONFAILED, Message: "Student roster validation failed.", Details: details}
	}

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "student_roster_imports"
	var targetID string
	evt := store.AuditEvent{
		Action:       domain.ActionStudentRosterImported,
		ActorAccount: stringPtrOrNil(actorAccount),
		ActorRole:    stringPtrOrNil(actorRole),
		TargetTable:  &targetTable,
		TargetID:     &targetID,
	}

	var result Result
	evt.PayloadFunc = func() ([]byte, error) {
		return json.Marshal(map[string]any{
			"source_filename": source,
			"total_rows":      totalRows,
			"valid_rows":      len(rows),
			"inserted_rows":   result.InsertedRows,
			"updated_rows":    result.UpdatedRows,
			"unchanged_rows":  result.UnchangedRows,
			"invalid_rows":    result.InvalidRows,
			"database_count":  result.DatabaseCount,
		})
	}
	err = s.store.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		written, writeErr := s.writer.Import(ctx, tx, ImportWrite{
			SourceFilename: source,
			Actor:          actor,
			Rows:           rows,
			TotalRows:      totalRows,
			InvalidRows:    invalidRows,
			Errors:         rowErrors,
		})
		if writeErr != nil {
			return false, writeErr
		}
		if written.ImportID == "" {
			return false, errors.New("student: import writer returned empty import id")
		}
		targetID = written.ImportID
		result = written
		return true, nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s *Service) parse(ctx context.Context, source io.Reader) (int, []ImportRow, int, []RowError, error) {
	r := csv.NewReader(source)
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		return 0, nil, 0, nil, validation("file", "empty", "CSV must include a header row.")
	}
	if err != nil {
		return 0, nil, 0, nil, validation("file", "invalid_csv", "CSV header could not be parsed.")
	}
	columns, err := mapHeaders(header)
	if err != nil {
		return 0, nil, 0, nil, err
	}

	var (
		totalRows   int
		invalidRows int
		rows        []ImportRow
		errs        []RowError
		seenRolls   = map[string]struct{}{}
	)
	for rowNumber := 2; ; rowNumber++ {
		record, readErr := r.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return 0, nil, 0, nil, validation("file", "invalid_csv", fmt.Sprintf("CSV row %d could not be parsed.", rowNumber))
		}
		totalRows++
		if totalRows > maxCSVRows {
			return 0, nil, 0, nil, validation("file", "too_many_rows", fmt.Sprintf("CSV must not contain more than %d data rows.", maxCSVRows))
		}
		rowErrs := make([]RowError, 0, 2)
		if len(record) != len(header) {
			invalidRows++
			errs = appendRowErrors(errs, rowError(rowNumber, "$row", "record_length", "Row has a different number of fields than the header."))
			if len(errs) > maxRowErrors {
				return 0, nil, 0, nil, validation("file", "too_many_errors", fmt.Sprintf("CSV has more than %d validation errors; fix the earlier rows and upload again.", maxRowErrors))
			}
			continue
		}
		cell := func(field string) string {
			index := columns[field]
			if index >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[index])
		}

		rawRoll := cell("roll_no")
		roll := strings.ToUpper(rawRoll)
		if rawRoll == "" {
			rowErrs = append(rowErrs, rowError(rowNumber, "roll_no", "required", "Roll number is required."))
		} else if !validText(rawRoll) {
			rowErrs = append(rowErrs, rowError(rowNumber, "roll_no", "invalid_encoding", "Roll number must be valid UTF-8 text without NUL bytes."))
		} else if utf8.RuneCountInString(roll) > maxRollRunes || !rollPattern.MatchString(roll) {
			rowErrs = append(rowErrs, rowError(rowNumber, "roll_no", "invalid", "Roll number contains unsupported characters."))
		} else if _, duplicate := seenRolls[roll]; duplicate {
			rowErrs = append(rowErrs, rowError(rowNumber, "roll_no", "duplicate", "Roll number appears more than once in this file."))
		}

		name := cell("name")
		rowErrs = append(rowErrs, validateRequired(rowNumber, "name", name, maxNameRunes)...)
		section := cell("section")
		rowErrs = append(rowErrs, validateRequired(rowNumber, "section", section, maxSectionRunes)...)
		batch := cell("batch")
		rowErrs = append(rowErrs, validateRequired(rowNumber, "batch", batch, maxBatchRunes)...)
		rawBranch := cell("branch")
		branchCode := strings.ToUpper(rawBranch)
		rowErrs = append(rowErrs, validateRequired(rowNumber, "branch", rawBranch, maxBranchRunes)...)

		var branch Branch
		if rawBranch != "" && validText(rawBranch) && utf8.RuneCountInString(branchCode) <= maxBranchRunes {
			resolved, branchErr := s.reader.ResolveBranch(ctx, branchCode)
			if errors.Is(branchErr, ErrBranchNotFound) {
				rowErrs = append(rowErrs, rowError(rowNumber, "branch", "unknown", fmt.Sprintf("Branch code %s does not exist.", branchCode)))
			} else if branchErr != nil {
				return 0, nil, 0, nil, branchErr
			} else {
				branch = resolved
			}
		}

		if len(rowErrs) > 0 {
			invalidRows++
			errs = appendRowErrors(errs, rowErrs...)
			if len(errs) > maxRowErrors {
				return 0, nil, 0, nil, validation("file", "too_many_errors", fmt.Sprintf("CSV has more than %d validation errors; fix the earlier rows and upload again.", maxRowErrors))
			}
			continue
		}
		seenRolls[roll] = struct{}{}
		rows = append(rows, ImportRow{RollNo: roll, Name: name, BranchID: branch.ID, Section: section, Batch: batch})
	}
	return totalRows, rows, invalidRows, errs, nil
}

func mapHeaders(header []string) (map[string]int, error) {
	columns := make(map[string]int, len(header))
	for index, raw := range header {
		if !validText(raw) {
			return nil, validation("header", "invalid_encoding", "CSV headers must be valid UTF-8 text without NUL bytes.")
		}
		name := headerField(raw)
		if name == "" {
			continue
		}
		if _, duplicate := columns[name]; duplicate {
			return nil, validation(name, "duplicate_header", "CSV has duplicate required headers.")
		}
		columns[name] = index
	}
	for _, field := range []string{"roll_no", "name", "branch", "section", "batch"} {
		if _, ok := columns[field]; !ok {
			return nil, validation(field, "required_header", "CSV is missing a required header.")
		}
	}
	return columns, nil
}

func headerField(raw string) string {
	trimmed := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	var compact strings.Builder
	for _, r := range strings.ToLower(trimmed) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			compact.WriteRune(r)
		}
	}
	switch compact.String() {
	case "rollno", "rollnumber":
		return "roll_no"
	case "name":
		return "name"
	case "branch":
		return "branch"
	case "section":
		return "section"
	case "batch":
		return "batch"
	default:
		return ""
	}
}

func validateRequired(row int, field, value string, limit int) []RowError {
	if value == "" {
		return []RowError{rowError(row, field, "required", strings.ReplaceAll(field, "_", " ")+" is required.")}
	}
	if !validText(value) {
		return []RowError{rowError(row, field, "invalid_encoding", fmt.Sprintf("%s must be valid UTF-8 text without NUL bytes.", strings.ReplaceAll(field, "_", " ")))}
	}
	if utf8.RuneCountInString(value) > limit {
		return []RowError{rowError(row, field, "maximum", fmt.Sprintf("%s must not exceed %d characters.", strings.ReplaceAll(field, "_", " "), limit))}
	}
	return nil
}

func validText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func appendRowErrors(errs []RowError, rowErrs ...RowError) []RowError {
	remaining := maxRowErrors + 1 - len(errs)
	if remaining <= 0 {
		return errs
	}
	if len(rowErrs) > remaining {
		rowErrs = rowErrs[:remaining]
	}
	return append(errs, rowErrs...)
}

func validation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Student roster validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func forbidden() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Administrator access is required."}
}

func rowError(row int, field, code, message string) RowError {
	return RowError{Row: row, Field: field, Code: code, Message: message}
}

func (e RowError) validationDetail() contracts.ValidationErrorDetail {
	return contracts.ValidationErrorDetail{Field: fmt.Sprintf("rows[%d].%s", e.Row, e.Field), Code: e.Code, Message: e.Message}
}

func stringPtrOrNil(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
