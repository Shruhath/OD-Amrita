package student

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func TestImport_NormalizesHeadersValuesAndCanonicalRolls(t *testing.T) {
	reader := fakeReader{branches: map[string]Branch{"CSE": {ID: "91000000-0000-4000-8000-000000000001", Code: "CSE"}}}
	writer := &fakeWriter{result: Result{ImportID: "77777777-7777-4777-8777-777777777777", InsertedRows: 2, DatabaseCount: 2}}
	svc := NewService(reader, fakeAuditStore{}, writer)

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
	reader := fakeReader{branches: map[string]Branch{"CSE": {ID: "91000000-0000-4000-8000-000000000001", Code: "CSE"}}}
	writer := &fakeWriter{result: Result{ImportID: "77777777-7777-4777-8777-777777777777", InsertedRows: 1, DatabaseCount: 1}}
	svc := NewService(reader, fakeAuditStore{}, writer)

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

func TestImport_RejectsZeroValidRowsBeforeWriter(t *testing.T) {
	writer := &fakeWriter{}
	svc := NewService(fakeReader{}, fakeAuditStore{}, writer)

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

func TestImport_RejectsNonAdminBeforeParsing(t *testing.T) {
	reader := fakeReader{err: errors.New("reader should not be called")}
	_, err := NewService(reader, fakeAuditStore{}, &fakeWriter{}).Import(context.Background(), Actor{Role: "student"}, ImportInput{
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

type fakeAuditStore struct{}

func (fakeAuditStore) WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error {
	_, err := write(ctx, nil)
	return err
}

func adminActor() Actor {
	return Actor{UserID: "90000000-0000-4000-8000-000000000001", Email: "admin@amrita.edu", Role: "admin"}
}

var _ io.Reader = strings.NewReader("")
