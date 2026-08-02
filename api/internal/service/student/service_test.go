package student

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func TestImport_NormalizesHeadersValuesAndCanonicalRolls(t *testing.T) {
	reader := &fakeReader{branches: map[string]Branch{"CSE": {ID: "91000000-0000-4000-8000-000000000001", Code: "CSE"}}}
	writer := &fakeWriter{result: Result{ImportID: "77777777-7777-4777-8777-777777777777", InsertedRows: 2, DatabaseCount: 2}}
	svc := NewService(reader, &fakeAuditStore{}, writer)

	csv := " batch ,BRANCH, name ,roll_no,Section\n2024, cse , Ananya José , cb.sc.u4cse24001 , A \n2024,CSE,Madhav Rao,CB.SC.U4CSE24002,B\n"
	result, err := svc.Import(context.Background(), Actor{UserID: "90000000-0000-4000-8000-000000000001", Email: "admin@amrita.edu", Role: "admin"}, ImportInput{
		FileName: " roster.csv ",
		Content:  strings.NewReader(csv),
	})
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}
	if result.TotalRows != 2 || result.InsertedRows != 2 || result.InvalidRows != 0 {
		t.Fatalf("unexpected result counts: %+v", result)
	}
	if len(writer.rows) != 2 {
		t.Fatalf("writer saw %d rows, want 2", len(writer.rows))
	}
	if got := writer.rows[0].RollNo; got != "CB.SC.U4CSE24001" {
		t.Errorf("canonical roll = %q", got)
	}
	if got := writer.rows[0].Name; got != "Ananya José" {
		t.Errorf("trimmed name = %q", got)
	}
	if got := writer.fileName; got != "roster.csv" {
		t.Errorf("filename = %q", got)
	}
}

func TestImport_CommitsValidRowsAndReportsInvalidRows(t *testing.T) {
	reader := &fakeReader{branches: map[string]Branch{"CSE": {ID: "91000000-0000-4000-8000-000000000001", Code: "CSE"}}}
	writer := &fakeWriter{result: Result{ImportID: "77777777-7777-4777-8777-777777777777", InsertedRows: 1, DatabaseCount: 1}}
	svc := NewService(reader, &fakeAuditStore{}, writer)

	valid := "CB.SC.U4CSE24010,Valid Student,CSE,A,2024\n"
	csv := "Roll No,Name,Branch,Section,Batch\n" +
		valid +
		"CB.SC.U4CSE24011,,CSE,A,2024\n" +
		"CB.SC.U4CSE24010,Duplicate Student,CSE,A,2024\n" +
		"CB.SC.U4CSE24012,Unknown Branch,ECE,A,2024\n" +
		"CB.SC.U4CSE24013," + strings.Repeat("N", 201) + ",CSE,A,2024\n" +
		"bad roll!,Bad Roll,CSE,A,2024\n" +
		"CB.SC.U4CSE24014,Missing Batch,CSE,A\n"

	result, err := svc.Import(context.Background(), adminActor(), ImportInput{FileName: "students.csv", Content: strings.NewReader(csv)})
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}
	if result.TotalRows != 7 || result.InsertedRows != 1 || result.InvalidRows != 6 {
		t.Fatalf("unexpected result counts: %+v", result)
	}
	if len(writer.rows) != 1 || writer.rows[0].RollNo != "CB.SC.U4CSE24010" {
		t.Fatalf("valid rows handed to writer = %+v", writer.rows)
	}
	want := map[string]bool{
		"name:required":      false,
		"roll_no:duplicate":  false,
		"branch:unknown":     false,
		"name:maximum":       false,
		"roll_no:invalid":    false,
		"$row:record_length": false,
	}
	for _, rowErr := range result.Errors {
		key := rowErr.Field + ":" + rowErr.Code
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("missing row error %s in %+v", key, result.Errors)
		}
	}
}

func TestImport_RejectsInvalidEncodingBeforeWriter(t *testing.T) {
	writer := &fakeWriter{}
	svc := NewService(
		&fakeReader{branches: map[string]Branch{"CSE": {ID: "91000000-0000-4000-8000-000000000001", Code: "CSE"}}},
		&fakeAuditStore{},
		writer,
	)

	csv := "Roll No,Name,Branch,Section,Batch\nCB.SC.U4CSE24020,Invalid\x00Name,CSE,A,2024\n"
	_, err := svc.Import(context.Background(), adminActor(), ImportInput{FileName: "students.csv", Content: strings.NewReader(csv)})

	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeVALIDATIONFAILED {
		t.Fatalf("error = %v, want VALIDATION_FAILED", err)
	}
	if writer.called {
		t.Fatal("writer was called for invalid encoded text")
	}
	if len(de.Details) != 1 || de.Details[0].Code != "invalid_encoding" {
		t.Fatalf("details = %+v, want invalid_encoding", de.Details)
	}
}

func TestImport_AcceptsCorrectedRollAfterEarlierInvalidRow(t *testing.T) {
	writer := &fakeWriter{result: Result{ImportID: "77777777-7777-4777-8777-777777777777", InsertedRows: 1, DatabaseCount: 1}}
	svc := NewService(
		&fakeReader{branches: map[string]Branch{"CSE": {ID: "91000000-0000-4000-8000-000000000001", Code: "CSE"}}},
		&fakeAuditStore{},
		writer,
	)

	csv := "Roll No,Name,Branch,Section,Batch\n" +
		"CB.SC.U4CSE24021,Bad Branch,UNKNOWN,A,2024\n" +
		"CB.SC.U4CSE24021,Corrected Student,CSE,A,2024\n"

	result, err := svc.Import(context.Background(), adminActor(), ImportInput{FileName: "students.csv", Content: strings.NewReader(csv)})
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}
	if result.InsertedRows != 1 || result.InvalidRows != 1 {
		t.Fatalf("counts = %+v, want one inserted and one invalid", result)
	}
	if len(writer.rows) != 1 || writer.rows[0].Name != "Corrected Student" {
		t.Fatalf("accepted rows = %+v, want corrected later row", writer.rows)
	}
}

func TestImport_RejectsPathologicalRowCount(t *testing.T) {
	var csv strings.Builder
	csv.WriteString("Roll No,Name,Branch,Section,Batch\n")
	for i := 0; i <= maxCSVRows; i++ {
		csv.WriteString(fmt.Sprintf("CB.SC.U4CSE%08d,Student %d,CSE,A,2024\n", i, i))
	}

	_, err := NewService(
		&fakeReader{branches: map[string]Branch{"CSE": {ID: "91000000-0000-4000-8000-000000000001", Code: "CSE"}}},
		&fakeAuditStore{},
		&fakeWriter{},
	).Import(context.Background(), adminActor(), ImportInput{
		FileName: "students.csv",
		Content:  strings.NewReader(csv.String()),
	})
	var de domain.DomainError
	if !errors.As(err, &de) || len(de.Details) == 0 || de.Details[0].Code != "too_many_rows" {
		t.Fatalf("error = %v, details=%+v, want too_many_rows", err, de.Details)
	}
}

func TestImport_RejectsZeroValidRowsBeforeWriter(t *testing.T) {
	writer := &fakeWriter{}
	svc := NewService(&fakeReader{}, &fakeAuditStore{}, writer)

	_, err := svc.Import(context.Background(), adminActor(), ImportInput{
		FileName: "students.csv",
		Content:  strings.NewReader("Roll No,Name,Branch,Section,Batch\n,No Roll,CSE,A,2024\n"),
	})
	var de domain.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("error = %v, want DomainError", err)
	}
	if de.Code != contracts.ErrorCodeVALIDATIONFAILED {
		t.Fatalf("code = %s, want VALIDATION_FAILED", de.Code)
	}
	if writer.called {
		t.Fatal("writer was called for a zero-valid-row import")
	}
}

func TestImport_AuditPayloadIncludesReconciliationCounts(t *testing.T) {
	audit := &fakeAuditStore{}
	writer := &fakeWriter{result: Result{
		ImportID:      "77777777-7777-4777-8777-777777777777",
		InsertedRows:  1,
		UpdatedRows:   2,
		UnchangedRows: 3,
		DatabaseCount: 901,
	}}
	svc := NewService(
		&fakeReader{branches: map[string]Branch{"CSE": {ID: "91000000-0000-4000-8000-000000000001", Code: "CSE"}}},
		audit,
		writer,
	)

	_, err := svc.Import(context.Background(), adminActor(), ImportInput{
		FileName: "students.csv",
		Content:  strings.NewReader("Roll No,Name,Branch,Section,Batch\nCB.SC.U4CSE24031,Student,CSE,A,2024\n"),
	})
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(audit.event.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v (%s)", err, string(audit.event.Payload))
	}
	for key, want := range map[string]float64{
		"total_rows":     1,
		"valid_rows":     1,
		"inserted_rows":  1,
		"updated_rows":   2,
		"unchanged_rows": 3,
		"invalid_rows":   0,
		"database_count": 901,
	} {
		if got := payload[key]; got != want {
			t.Errorf("payload[%s] = %v, want %v", key, got, want)
		}
	}
}

func TestImport_RejectsNonAdminBeforeParsing(t *testing.T) {
	reader := &fakeReader{err: errors.New("reader should not be called")}
	_, err := NewService(reader, &fakeAuditStore{}, &fakeWriter{}).Import(context.Background(), Actor{Role: "student"}, ImportInput{
		FileName: "students.csv",
		Content:  strings.NewReader("Roll No,Name,Branch,Section,Batch\nCB.SC.U4CSE24001,Name,CSE,A,2024\n"),
	})
	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeRBACFORBIDDEN {
		t.Fatalf("error = %v, want RBAC_FORBIDDEN", err)
	}
}

type fakeReader struct {
	branches map[string]Branch
	err      error

	// students backs ListStudents. listCalls records how many times the roster
	// read was reached so a test can prove the RBAC/validation gates ran first.
	students  []Student
	listInput *ListInput
	listCalls int
	listErr   error
}

func (f fakeReader) ResolveBranch(ctx context.Context, code string) (Branch, error) {
	if f.err != nil {
		return Branch{}, f.err
	}
	branch, ok := f.branches[strings.ToUpper(strings.TrimSpace(code))]
	if !ok {
		return Branch{}, ErrBranchNotFound
	}
	return branch, nil
}

func (f *fakeReader) ListStudents(ctx context.Context, input ListInput) ([]Student, int, error) {
	f.listCalls++
	captured := input
	f.listInput = &captured
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	if f.err != nil {
		return nil, 0, f.err
	}
	return append([]Student(nil), f.students...), len(f.students), nil
}

type fakeWriter struct {
	called   bool
	rows     []ImportRow
	fileName string
	result   Result
}

func (f *fakeWriter) Import(ctx context.Context, tx pgx.Tx, input ImportWrite) (Result, error) {
	f.called = true
	f.rows = append([]ImportRow(nil), input.Rows...)
	f.fileName = input.SourceFilename
	result := f.result
	result.TotalRows = input.TotalRows
	result.InvalidRows = input.InvalidRows
	result.Errors = input.Errors
	return result, nil
}

type fakeAuditStore struct {
	event store.AuditEvent
}

func (f *fakeAuditStore) WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error {
	changed, err := write(ctx, nil)
	if err != nil {
		return err
	}
	if changed && evt.PayloadFunc != nil {
		payload, err := evt.PayloadFunc()
		if err != nil {
			return err
		}
		evt.Payload = payload
	}
	f.event = evt
	return nil
}

func adminActor() Actor {
	return Actor{UserID: "90000000-0000-4000-8000-000000000001", Email: "admin@amrita.edu", Role: "admin"}
}

var _ io.Reader = strings.NewReader("")
