//go:build integration

package student

import (
	"fmt"
	"strings"
	"testing"

	studentsvc "github.com/Shruhath/OD-Amrita/api/internal/service/student"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

// Roster search, against the real query.
//
// THE DEFECT THIS FILE EXISTS FOR. The search was prefix-only, so it answered
// only the one shape of question nobody actually asks: the first word of a full
// name, spelled from its first letter. A coordinator looking for "Shruhat
// Reddy" who typed the surname — or the two words the other way round, or the
// tail of a roll number — was told there was no such student, which reads as the
// roster being incomplete rather than as the search being narrow.
//
// These run against Postgres deliberately. The service tests observe the
// PARAMETERS the reader is handed, which is the right seam for escaping and
// bounds but cannot tell you whether `~~ ALL` over an array actually matches the
// row — and that predicate is the whole of the fix.

// seedSearchRoster imports names chosen so that each assertion below can only
// pass for the intended reason. Returns the run's roll prefix.
func seedSearchRoster(t *testing.T, svc *studentsvc.Service) string {
	t.Helper()
	prefix := uniquePrefix()
	// "Ashruhathan" CONTAINS "shruhat" without starting a word with it — it is
	// what makes the ranking assertion mean something, and it is also why the
	// surname search cannot pass by matching the wrong row.
	csv := fmt.Sprintf(
		"Roll No,Name,Branch,Section,Batch\n"+
			"%s101,Shruhat Reddy,CSE,A,2024\n"+
			"%s102,Ashruhathan Kumar,CSE,A,2024\n"+
			"%s103,Reddy Mahesh,CSE,A,2024\n",
		prefix, prefix, prefix)

	if _, err := svc.Import(testCtx(t), adminActor(), studentsvc.ImportInput{
		FileName: "search-roster.csv",
		Content:  strings.NewReader(csv),
	}); err != nil {
		t.Fatalf("seed search roster: %v", err)
	}
	return prefix
}

func newSearchService(t *testing.T) (*studentsvc.Service, string) {
	t.Helper()
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))
	runtime := connect(t, ctx, dsnOrSkip(t, envRuntimeDSN))
	seed(t, ctx, migrator)

	// Built on the RUNTIME role, not the migrator: the API searches as
	// od_runtime in production, so the read must be provably within that role's
	// privileges rather than only working as the owner.
	svc := studentsvc.NewService(studentsvc.NewSQLCReader(runtime), store.New(runtime), studentsvc.NewSQLCWriter()).
		WithSearchReader(studentsvc.NewSQLCSearchReader(runtime))
	return svc, seedSearchRoster(t, svc)
}

func coordinator() studentsvc.Actor {
	return studentsvc.Actor{UserID: adminUserID, Email: "student-import-admin@amrita.edu", Role: "coordinator"}
}

// rollsFound reduces a result set to the roll numbers from THIS run. The
// database is shared between integration tests, so an absolute result list would
// depend on which other suites have run.
func rollsFound(t *testing.T, svc *studentsvc.Service, query, prefix string) []string {
	t.Helper()
	// 50 — the cap — so a "this student is missing" assertion is about the match,
	// never about a row being pushed past the default limit by leftover data.
	results, err := svc.Search(testCtx(t), coordinator(), studentsvc.SearchInput{Query: query, Limit: 50})
	if err != nil {
		t.Fatalf("Search(%q): %v", query, err)
	}
	out := make([]string, 0, len(results))
	for _, item := range results {
		if strings.HasPrefix(item.RollNo, prefix) {
			out = append(out, item.RollNo)
		}
	}
	return out
}

func assertFinds(t *testing.T, got []string, wantRoll, why string) {
	t.Helper()
	for _, roll := range got {
		if roll == wantRoll {
			return
		}
	}
	t.Errorf("[P0] %s — %s was not in the results %v", why, wantRoll, got)
}

// SEARCH-DB-001 — THE REPORTED DEFECT. A surname finds the student.
func TestSearchFindsAStudentByAnyWordOfTheirName(t *testing.T) {
	svc, prefix := newSearchService(t)

	for _, tc := range []struct{ query, why string }{
		{"Reddy", "a surname is the word people type when the first name is the one they are unsure of"},
		{"Shruhat", "the first name must keep working"},
		{"reddy", "search is case-insensitive"},
		{"REDDY", "search is case-insensitive"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			assertFinds(t, rollsFound(t, svc, tc.query, prefix), prefix+"101", tc.why)
		})
	}
}

// SEARCH-DB-002 — both words, in EITHER order.
//
// "Reddy Shruhat" is the same question as "Shruhat Reddy". A roster stores one
// of those orders and a person types whichever they think of first.
func TestSearchMatchesEveryWordInAnyOrder(t *testing.T) {
	svc, prefix := newSearchService(t)

	for _, query := range []string{"Shruhat Reddy", "Reddy Shruhat", "  reddy   shruhat  "} {
		t.Run(query, func(t *testing.T) {
			assertFinds(t, rollsFound(t, svc, query, prefix), prefix+"101", "every word matches somewhere in the name, so word order cannot matter")
		})
	}
}

// SEARCH-DB-003 — the tail of a roll number finds the student.
//
// Rolls are long and share a long common head (`CB.SC.U4CSE…`); the part that
// identifies a person is at the END, which is exactly the part a prefix search
// could never match.
func TestSearchMatchesTheTailOfARollNumber(t *testing.T) {
	svc, prefix := newSearchService(t)

	tail := prefix[len(prefix)-5:] + "101"
	assertFinds(t, rollsFound(t, svc, tail, prefix), prefix+"101",
		"the identifying part of a roll number is its tail")
}

// SEARCH-DB-004 — TERMS NARROW, THEY NEVER WIDEN.
//
// The security-relevant half of the change. Matching "any word" must mean every
// word must match, not any word may match: the second reading turns a two-word
// query into a union and makes a long query return MORE of the roster.
func TestSearchRequiresEveryTermToMatch(t *testing.T) {
	svc, prefix := newSearchService(t)

	if got := rollsFound(t, svc, "Reddy Nonexistent", prefix); len(got) != 0 {
		t.Errorf("[P0] a term matching nobody still returned %v — terms are ANDed, so this must be empty", got)
	}
	// And the narrowing is real, not incidental: "Reddy" alone finds two of this
	// run's students, "Reddy Mahesh" finds one of them.
	if broad, narrow := rollsFound(t, svc, "Reddy", prefix), rollsFound(t, svc, "Reddy Mahesh", prefix); len(narrow) >= len(broad) {
		t.Errorf("[P0] adding a term did not narrow the result: %q gave %v, %q gave %v", "Reddy", broad, "Reddy Mahesh", narrow)
	}
}

// SEARCH-DB-005 — a word-START match outranks a mid-word one.
//
// This is what the prefix search got right and what a naive containment search
// would lose: typing a first name must still put that person at the top, not
// behind everyone who happens to contain the letters.
func TestSearchRanksWordStartMatchesFirst(t *testing.T) {
	svc, prefix := newSearchService(t)

	got := rollsFound(t, svc, "Shruhat", prefix)
	if len(got) != 2 {
		t.Fatalf("[P0] want both Shruhat Reddy and Ashruhathan Kumar, got %v", got)
	}
	if got[0] != prefix+"101" {
		t.Errorf("[P0] ranked %v first; Shruhat Reddy (%s101) starts a word with the query and must outrank "+
			"Ashruhathan Kumar, which only contains it", got[0], prefix)
	}
}

// SEARCH-DB-006 — A WILDCARD IS STILL JUST A CHARACTER.
//
// The minimum-length guard exists to stop a roster dump; escaping is what stops
// a three-character query from being one. `%re` matches a literal per-cent sign
// followed by "re" — which no name has — so an empty result is the proof. If the
// escaping were dropped it would match every name containing "re".
func TestSearchTreatsWildcardsAsLiteralCharacters(t *testing.T) {
	svc, _ := newSearchService(t)

	results, err := svc.Search(testCtx(t), coordinator(), studentsvc.SearchInput{Query: "%re", Limit: 50})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("[P0] `%%re` returned %d rows — an unescaped %% makes any short query a roster dump", len(results))
	}
}
