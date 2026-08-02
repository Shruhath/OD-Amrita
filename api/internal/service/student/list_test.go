package student

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

func rosterFixture() []Student {
	return []Student{
		{StudentID: "a1000000-0000-4000-8000-000000000001", RollNo: "CB.SC.U4CSE24001", DisplayName: "Ananya José", Branch: "CSE", Section: "A", Batch: "2024"},
		{StudentID: "a1000000-0000-4000-8000-000000000002", RollNo: "CB.SC.U4CSE24002", DisplayName: "Madhav Rao", Branch: "CSE", Section: "A", Batch: "2024"},
	}
}

func listService(reader *fakeReader) *Service {
	return NewService(reader, &fakeAuditStore{}, &fakeWriter{})
}

func TestList_FiltersOrdersAndPaginates(t *testing.T) {
	reader := &fakeReader{students: rosterFixture()}
	svc := listService(reader)

	result, err := svc.List(context.Background(), adminActor(), ListInput{
		BranchID: "91000000-0000-4000-8000-000000000001",
		Batch:    "2024",
		Section:  "A",
		Page:     1,
		PageSize: 100,
	})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if result.Page != 1 || result.PageSize != 100 {
		t.Fatalf("page metadata = %+v, want page 1 size 100", result)
	}
	if result.Total != 2 {
		t.Fatalf("total = %d, want 2", result.Total)
	}
	if len(result.Students) != 2 {
		t.Fatalf("students = %d, want deterministic roster rows", len(result.Students))
	}
	if result.Students[0].RollNo > result.Students[1].RollNo {
		t.Fatalf("students not ordered by canonical roll number: %+v", result.Students)
	}

	// The filters must reach the reader verbatim — a filter dropped between the
	// handler and the query would silently return the WRONG section's roster,
	// which during population means writing a card for the wrong student.
	if reader.listInput == nil {
		t.Fatal("reader never received the list input")
	}
	if got := *reader.listInput; got.BranchID != "91000000-0000-4000-8000-000000000001" || got.Batch != "2024" || got.Section != "A" {
		t.Fatalf("reader filters = %+v, want branch/batch/section preserved", got)
	}
}

func TestList_RejectsNonAdminBeforeLookup(t *testing.T) {
	reader := &fakeReader{students: rosterFixture()}

	_, err := listService(reader).List(context.Background(), Actor{Role: "student"}, ListInput{Page: 1, PageSize: 100})

	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeRBACFORBIDDEN {
		t.Fatalf("error = %v, want RBAC_FORBIDDEN", err)
	}
	if reader.listCalls != 0 {
		t.Fatalf("reader was called %d times for a non-admin caller, want 0", reader.listCalls)
	}
}

// A non-admin must be refused before validation too. If validation ran first, a
// student could probe which filter values the roster accepts by reading the
// 400-vs-403 difference — the enumeration leak Story 0.3's review closed on the
// card-token handlers.
func TestList_RejectsNonAdminBeforeValidation(t *testing.T) {
	reader := &fakeReader{students: rosterFixture()}

	_, err := listService(reader).List(context.Background(), Actor{Role: "teacher"}, ListInput{
		BranchID: "not-a-uuid",
		Page:     -5,
		PageSize: 9999,
	})

	var de domain.DomainError
	if !errors.As(err, &de) || de.Code != contracts.ErrorCodeRBACFORBIDDEN {
		t.Fatalf("error = %v, want RBAC_FORBIDDEN ahead of validation", err)
	}
	if reader.listCalls != 0 {
		t.Fatalf("reader was called %d times, want 0", reader.listCalls)
	}
}

func TestList_DefaultsPagination(t *testing.T) {
	reader := &fakeReader{students: rosterFixture()}

	result, err := listService(reader).List(context.Background(), adminActor(), ListInput{})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if result.Page != 1 || result.PageSize != defaultListPageSize {
		t.Fatalf("defaults = page %d size %d, want page 1 size %d", result.Page, result.PageSize, defaultListPageSize)
	}
	if reader.listInput.Page != 1 || reader.listInput.PageSize != defaultListPageSize {
		t.Fatalf("reader received %+v, want the normalized defaults", *reader.listInput)
	}
}

func TestList_RejectsPaginationAndFilterBounds(t *testing.T) {
	cases := []struct {
		name  string
		input ListInput
		field string
		code  string
	}{
		{"page below one", ListInput{Page: 0 - 1}, "page", "minimum"},
		{"page size below one", ListInput{PageSize: -1}, "page_size", "minimum"},
		{"page size above max", ListInput{PageSize: maxListPageSize + 1}, "page_size", "maximum"},
		{"branch id not a uuid", ListInput{BranchID: "not-a-uuid"}, "branch_id", "invalid_format"},
		{"batch too long", ListInput{Batch: strings.Repeat("b", maxBatchRunes+1)}, "batch", "maximum"},
		{"section too long", ListInput{Section: strings.Repeat("s", maxSectionRunes+1)}, "section", "maximum"},
		{"batch with NUL", ListInput{Batch: "20\x0024"}, "batch", "invalid_encoding"},
		{"section with NUL", ListInput{Section: "A\x00"}, "section", "invalid_encoding"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := &fakeReader{students: rosterFixture()}

			_, err := listService(reader).List(context.Background(), adminActor(), tc.input)

			var de domain.DomainError
			if !errors.As(err, &de) || de.Code != contracts.ErrorCodeVALIDATIONFAILED {
				t.Fatalf("error = %v, want VALIDATION_FAILED", err)
			}
			if len(de.Details) != 1 || de.Details[0].Field != tc.field || de.Details[0].Code != tc.code {
				t.Fatalf("details = %+v, want %s:%s", de.Details, tc.field, tc.code)
			}
			if reader.listCalls != 0 {
				t.Fatalf("reader was called despite invalid input")
			}
		})
	}
}

func TestList_NormalizesFiltersBeforeQuerying(t *testing.T) {
	reader := &fakeReader{students: rosterFixture()}

	_, err := listService(reader).List(context.Background(), adminActor(), ListInput{
		// Uppercase UUID and padded filters — all must reach the query in the
		// one canonical form the column actually stores.
		BranchID: "  91000000-0000-4000-8000-00000000000A  ",
		Batch:    "  2024  ",
		Section:  "  A  ",
	})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	got := *reader.listInput
	if got.BranchID != "91000000-0000-4000-8000-00000000000a" {
		t.Errorf("branch id = %q, want canonical lowercase uuid", got.BranchID)
	}
	if got.Batch != "2024" || got.Section != "A" {
		t.Errorf("filters = %q/%q, want trimmed", got.Batch, got.Section)
	}
}

func TestList_BlankFiltersStayUnfiltered(t *testing.T) {
	reader := &fakeReader{students: rosterFixture()}

	_, err := listService(reader).List(context.Background(), adminActor(), ListInput{
		BranchID: "   ",
		Batch:    "   ",
		Section:  "\t",
	})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	got := *reader.listInput
	if got.BranchID != "" || got.Batch != "" || got.Section != "" {
		t.Fatalf("whitespace-only filters = %+v, want the unfiltered sentinel", got)
	}
}

func TestList_ReturnsEmptySliceNotNil(t *testing.T) {
	reader := &fakeReader{}

	result, err := listService(reader).List(context.Background(), adminActor(), ListInput{})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if result.Students == nil {
		t.Fatal("students = nil, want an empty slice so the JSON body is [] not null")
	}
	if len(result.Students) != 0 || result.Total != 0 {
		t.Fatalf("result = %+v, want an empty page", result)
	}
}

func TestList_PropagatesReaderErrors(t *testing.T) {
	sentinel := errors.New("roster read failed")
	reader := &fakeReader{listErr: sentinel}

	_, err := listService(reader).List(context.Background(), adminActor(), ListInput{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the reader error propagated", err)
	}
}

// TestStudentListItemExposesNoPrivateFields is a structural guard, not a value
// check. Asserting `student.Email == ""` would still permit a type that CARRIES
// an email — one careless mapping away from serialising it. Asserting the field
// does not exist makes the leak impossible to write.
func TestStudentListItemExposesNoPrivateFields(t *testing.T) {
	forbidden := map[string]bool{
		"email": true, "token": true, "cardtoken": true, "payload": true,
		"hmac": true, "hmackey": true, "key": true, "tokensha256": true,
		"fingerprint": true, "version": true,
	}

	typ := reflect.TypeOf(Student{})
	for i := range typ.NumField() {
		name := strings.ToLower(typ.Field(i).Name)
		if forbidden[name] {
			t.Errorf("student.Student must not carry a %q field", typ.Field(i).Name)
		}
	}

	want := []string{"StudentID", "RollNo", "DisplayName", "Branch", "Section", "Batch"}
	got := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		got = append(got, typ.Field(i).Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("student.Student fields = %v, want exactly %v", got, want)
	}
}
