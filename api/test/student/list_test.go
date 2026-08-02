//go:build integration

package student

import (
	"fmt"
	"strings"
	"testing"

	studentsvc "github.com/Shruhath/OD-Amrita/api/internal/service/student"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// seedRoster imports a two-section roster and returns the roll-number prefix so
// assertions can isolate this run's rows from other tests sharing the database.
func seedRoster(t *testing.T, svc *studentsvc.Service) string {
	t.Helper()
	prefix := uniquePrefix()
	// Deliberately supplied OUT of roll order and across two sections, so an
	// ordering assertion cannot pass by accident of insertion order.
	csv := fmt.Sprintf(
		"Roll No,Name,Branch,Section,Batch\n"+
			"%s003,Third Student,CSE,A,2024\n"+
			"%s001,First Student,CSE,A,2024\n"+
			"%s002,Second Student,CSE,A,2024\n"+
			"%s010,Section B Student,CSE,B,2024\n"+
			"%s020,Older Batch Student,CSE,A,2023\n",
		prefix, prefix, prefix, prefix, prefix)

	if _, err := svc.Import(testCtx(t), adminActor(), studentsvc.ImportInput{
		FileName: "students.csv",
		Content:  strings.NewReader(csv),
	}); err != nil {
		t.Fatalf("seed roster: %v", err)
	}
	return prefix
}

func rowsWithPrefix(students []studentsvc.Student, prefix string) []studentsvc.Student {
	out := make([]studentsvc.Student, 0, len(students))
	for _, item := range students {
		if strings.HasPrefix(item.RollNo, prefix) {
			out = append(out, item)
		}
	}
	return out
}

func TestStudentListUsesDatabaseOrderingAndRuntimeReadPrivileges(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	seed(t, ctx, migrator)

	// The service is built on the RUNTIME role, not the migrator: the populator
	// runs as od_runtime in production, so the read must be provably within that
	// role's privileges rather than only working as the owner.
	svc := studentsvc.NewService(studentsvc.NewSQLCReader(runtime), store.New(runtime), studentsvc.NewSQLCWriter())
	prefix := seedRoster(t, svc)

	result, err := svc.List(ctx, adminActor(), studentsvc.ListInput{
		BranchID: branchID,
		Batch:    "2024",
		Section:  "A",
		Page:     1,
		PageSize: 100,
	})
	if err != nil {
		t.Fatalf("list students: %v", err)
	}

	mine := rowsWithPrefix(result.Students, prefix)
	if len(mine) != 3 {
		t.Fatalf("students = %d, want the 3 seeded 2024/section-A rows: %+v", len(mine), mine)
	}
	for i := 1; i < len(mine); i++ {
		if mine[i-1].RollNo > mine[i].RollNo {
			t.Fatalf("students not returned in canonical roll order: %+v", mine)
		}
	}
	for _, item := range mine {
		if item.Section != "A" || item.Batch != "2024" {
			t.Errorf("filter leaked a non-matching row: %+v", item)
		}
		if item.Branch != "CSE" {
			t.Errorf("branch = %q, want the joined branch CODE not an id", item.Branch)
		}
		if item.StudentID == "" || item.DisplayName == "" {
			t.Errorf("row is missing display identity: %+v", item)
		}
	}
}

func TestStudentListFiltersAreIndependentAndTotalMatchesFilters(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	seed(t, ctx, migrator)

	svc := studentsvc.NewService(studentsvc.NewSQLCReader(runtime), store.New(runtime), studentsvc.NewSQLCWriter())
	prefix := seedRoster(t, svc)

	cases := []struct {
		name  string
		input studentsvc.ListInput
		want  int
	}{
		{"section B only", studentsvc.ListInput{BranchID: branchID, Section: "B", Batch: "2024"}, 1},
		{"older batch only", studentsvc.ListInput{BranchID: branchID, Section: "A", Batch: "2023"}, 1},
		{"whole 2024 batch", studentsvc.ListInput{BranchID: branchID, Batch: "2024"}, 4},
		{"whole branch", studentsvc.ListInput{BranchID: branchID}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := svc.List(ctx, adminActor(), tc.input)
			if err != nil {
				t.Fatalf("list students: %v", err)
			}
			if got := len(rowsWithPrefix(result.Students, prefix)); got != tc.want {
				t.Fatalf("rows = %d, want %d", got, tc.want)
			}
		})
	}
}

// The total must describe the FILTERED set, not the table. A total taken from a
// bare count(*) would make the populator's "n of m written" progress read
// against every student in the institution.
func TestStudentListTotalCountsOnlyTheFilteredSet(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	seed(t, ctx, migrator)

	svc := studentsvc.NewService(studentsvc.NewSQLCReader(runtime), store.New(runtime), studentsvc.NewSQLCWriter())
	seedRoster(t, svc)

	sectionB, err := svc.List(ctx, adminActor(), studentsvc.ListInput{BranchID: branchID, Section: "B", Batch: "2024"})
	if err != nil {
		t.Fatalf("list section B: %v", err)
	}
	unfiltered, err := svc.List(ctx, adminActor(), studentsvc.ListInput{})
	if err != nil {
		t.Fatalf("list unfiltered: %v", err)
	}
	if sectionB.Total >= unfiltered.Total {
		t.Fatalf("filtered total = %d, unfiltered total = %d — the count ignores the filters", sectionB.Total, unfiltered.Total)
	}
	if sectionB.Total != len(sectionB.Students) {
		t.Fatalf("total = %d but page returned %d rows for a single-page result", sectionB.Total, len(sectionB.Students))
	}
}

// Paging must partition the roster: no row may be skipped or repeated across
// pages. During population a repeat wastes a card and a skip leaves a student
// unwritten, and neither is visible to the operator.
func TestStudentListPagesPartitionTheRosterWithoutGapsOrRepeats(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	seed(t, ctx, migrator)

	svc := studentsvc.NewService(studentsvc.NewSQLCReader(runtime), store.New(runtime), studentsvc.NewSQLCWriter())
	prefix := seedRoster(t, svc)

	// Page to exhaustion rather than a fixed page count: this package's other
	// tests seed the same branch, so the roster is larger than this test's own
	// rows and a fixed window would miss some of them.
	const pageSize = 2
	seen := map[string]int{}
	pages := 0
	for page := 1; ; page++ {
		result, err := svc.List(ctx, adminActor(), studentsvc.ListInput{
			BranchID: branchID,
			Page:     page,
			PageSize: pageSize,
		})
		if err != nil {
			t.Fatalf("list page %d: %v", page, err)
		}
		if len(result.Students) > pageSize {
			t.Fatalf("page %d returned %d rows, want at most the page size %d", page, len(result.Students), pageSize)
		}
		for _, item := range rowsWithPrefix(result.Students, prefix) {
			seen[item.RollNo]++
		}
		pages++
		if len(result.Students) == 0 || page*pageSize >= result.Total {
			break
		}
		if pages > 1000 {
			t.Fatal("paging did not terminate — the offset is not advancing")
		}
	}

	if pages < 2 {
		t.Fatalf("only %d page was fetched; the partition assertion needs multiple pages", pages)
	}
	if len(seen) != 5 {
		t.Fatalf("paging surfaced %d distinct seeded rows, want all 5", len(seen))
	}
	for roll, count := range seen {
		if count != 1 {
			t.Errorf("%s appeared on %d pages, want exactly 1", roll, count)
		}
	}
}

func TestStudentListRejectsNonAdminAgainstRealDatabase(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	seed(t, ctx, migrator)

	svc := studentsvc.NewService(studentsvc.NewSQLCReader(runtime), store.New(runtime), studentsvc.NewSQLCWriter())

	if _, err := svc.List(ctx, studentsvc.Actor{UserID: adminUserID, Role: "student"}, studentsvc.ListInput{}); err == nil {
		t.Fatal("a student-role actor read the roster")
	}
}
