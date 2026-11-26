// Tests for the authenticated student search (FR55).
//
// Search is the coordinator's fallback when a card will not tap and the camera
// will not decode, and it is also the resolution step for the camera path. It is
// therefore on the critical path twice.
//
// Two properties matter more than the happy path:
//
//  1. EVERY WORD MATCHES, ANYWHERE, IN ANY ORDER — and the terms are ANDed, so
//     a longer query can only ever return fewer rows. This was prefix-only,
//     which meant a coordinator who typed a surname was told the student did
//     not exist. What has to stay true through the looser matching is the
//     bounding: escaped metacharacters, a minimum length, a server-side cap,
//     and never an empty term list (an empty `ALL(...)` matches all 1,534 rows).
//  2. The response carries no email, token or fingerprint. `students` has no
//     email column and card tokens are stored as fingerprints only — this
//     keeps both facts true through a new read path.
package student

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// searchCapture records the arguments the service hands the SQLC reader, which
// is where the term-splitting, escaping and limit decisions become observable
// without a database.
type searchCapture struct {
	queries []SearchQuery
	results []SearchResult
	err     error
}

func (c *searchCapture) SearchStudents(_ context.Context, q SearchQuery) ([]SearchResult, error) {
	c.queries = append(c.queries, q)
	return c.results, c.err
}

func newSearchService(capture *searchCapture) *Service {
	svc := NewService(nil, nil, nil)
	svc.WithSearchReader(capture)
	return svc
}

func coordinatorActor() Actor { return Actor{UserID: "coord-1", Role: "coordinator"} }

// 0.6-SEARCH-001a — `q` is trimmed first, then length-checked. A user who types
// three spaces has not typed a query, and must get a typed validation detail
// rather than an unbounded scan.
func TestSearch_RejectsShortQueryAfterTrimming(t *testing.T) {
	for _, raw := range []string{"", " ", "ab", "  ab  ", "\t\n"} {
		t.Run(strings.TrimSpace(raw)+"|", func(t *testing.T) {
			capture := &searchCapture{}
			svc := newSearchService(capture)

			_, err := svc.Search(context.Background(), coordinatorActor(), SearchInput{Query: raw})
			if err == nil {
				t.Fatalf("[P0] Search(%q) succeeded — fewer than 3 characters after trimming must be rejected", raw)
			}
			if len(capture.queries) != 0 {
				t.Errorf("[P0] Search(%q) reached the database despite failing validation", raw)
			}
		})
	}
}

// 0.6-SEARCH-001b — one containment pattern PER WORD, so any word of a name
// finds the student and word order does not matter.
//
// This replaced a prefix-only assertion. Prefix-only answered exactly one shape
// of question — the first word of a name, spelled from its first letter — and
// told a coordinator searching a surname that no such student existed. The index
// argument that justified it did not survive contact: the query already planned
// as a sequential scan under a non-C collation, which is what the roster
// actually runs. See students.sql for what replaces it at scale (pg_trgm).
func TestSearch_MatchesEveryWordAnywhere(t *testing.T) {
	capture := &searchCapture{}
	svc := newSearchService(capture)

	if _, err := svc.Search(context.Background(), coordinatorActor(), SearchInput{Query: "Reddy Shruhat"}); err != nil {
		t.Fatalf("Search = %v, want nil", err)
	}
	if len(capture.queries) != 1 {
		t.Fatalf("[P0] reader called %d times, want 1", len(capture.queries))
	}

	got := capture.queries[0]
	if want := []string{"%reddy%", "%shruhat%"}; !reflect.DeepEqual(got.Patterns, want) {
		t.Errorf("[P0] Patterns = %v, want %v — one lowercased containment pattern per word", got.Patterns, want)
	}
	for _, pattern := range got.Patterns {
		if !strings.HasPrefix(pattern, "%") || !strings.HasSuffix(pattern, "%") {
			t.Errorf("[P0] pattern %q is not a containment pattern — a surname cannot match a full name without one", pattern)
		}
	}
	// The head pattern is the RANKING term, not a filter: it is what keeps a
	// word-start match above a mid-word one now that mid-word matches exist.
	if got.HeadPattern != "% reddy shruhat%" {
		t.Errorf("[P0] HeadPattern = %q, want %q", got.HeadPattern, "% reddy shruhat%")
	}
}

// 0.6-SEARCH-001b2 — THE ARRAY IS NEVER EMPTY.
//
// `x ~~ ALL (ARRAY[]::text[])` is vacuously TRUE, so an empty pattern list does
// not return nothing — it returns the entire roster of 1,534 real people. That
// makes "at least one term" a security property, not a tidiness one.
func TestSearch_NeverBuildsAnEmptyTermList(t *testing.T) {
	for _, raw := range []string{"abc", "  abc  ", "a b c", "Reddy Shruhat", "  Reddy   Shruhat  "} {
		t.Run(raw, func(t *testing.T) {
			capture := &searchCapture{}
			svc := newSearchService(capture)

			if _, err := svc.Search(context.Background(), coordinatorActor(), SearchInput{Query: raw}); err != nil {
				t.Fatalf("Search = %v, want nil", err)
			}
			if len(capture.queries[0].Patterns) == 0 {
				t.Errorf("[P0] query %q produced no terms — an empty ALL(...) matches every row in the roster", raw)
			}
		})
	}
}

// 0.6-SEARCH-001c — `%` and `_` are LIKE metacharacters. Unescaped, a coordinator
// typing `_` matches every single-character position, and `%` matches the whole
// roster — which is exactly the roster dump the minLength guard exists to stop.
func TestSearch_EscapesLikeMetacharacters(t *testing.T) {
	for _, raw := range []string{"cse%", "cb_sc", "100%_", `back\slash`} {
		t.Run(raw, func(t *testing.T) {
			capture := &searchCapture{}
			svc := newSearchService(capture)

			if _, err := svc.Search(context.Background(), coordinatorActor(), SearchInput{Query: raw}); err != nil {
				t.Fatalf("Search = %v, want nil", err)
			}
			for _, got := range capture.queries[0].Patterns {
				// The leading and trailing `%` are OURS. Everything between them
				// came from the user and must be inert.
				body := strings.TrimSuffix(strings.TrimPrefix(got, "%"), "%")

				if strings.Contains(body, "%") && !strings.Contains(body, `\%`) {
					t.Errorf("[P0] pattern %q carries an unescaped %% — a wildcard in user input must not become a wildcard in the query", got)
				}
				if strings.Contains(body, "_") && !strings.Contains(body, `\_`) {
					t.Errorf("[P0] pattern %q carries an unescaped _ — LIKE treats it as a single-character wildcard", got)
				}
			}
		})
	}
}

// 0.6-SEARCH-001d — the limit is decided by the server. A client asking for
// 10,000 rows gets the cap, not the ask.
func TestSearch_BoundsTheLimitServerSide(t *testing.T) {
	cases := []struct {
		name    string
		request int
		wantMax int
	}{
		{"absent means default", 0, 20},
		{"in range is honoured", 10, 10},
		{"over the cap is clamped", 10_000, 50},
		{"negative is not a limit", -1, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := &searchCapture{}
			svc := newSearchService(capture)

			if _, err := svc.Search(context.Background(), coordinatorActor(), SearchInput{Query: "cse", Limit: tc.request}); err != nil {
				t.Fatalf("Search = %v, want nil", err)
			}
			got := capture.queries[0].Limit
			if got <= 0 || got > 50 {
				t.Fatalf("[P0] limit = %d, want a bounded 1..50 value", got)
			}
			if tc.request > 0 && tc.request <= 50 && got != tc.request {
				t.Errorf("[P0] limit = %d, want the requested %d", got, tc.request)
			}
			if got > tc.wantMax {
				t.Errorf("[P0] limit = %d, want at most %d", got, tc.wantMax)
			}
		})
	}
}

// 0.6-SEARCH-001e — ordering is exact-roll-first. A coordinator who types a full
// roll number is looking at ONE person and must not have to scan a list for
// them. Determinism matters too: `id` is the final tiebreaker so a repeated
// search cannot reorder under the coordinator's finger.
func TestSearch_OrdersExactRollMatchFirstAndDeterministically(t *testing.T) {
	capture := &searchCapture{}
	svc := newSearchService(capture)

	if _, err := svc.Search(context.Background(), coordinatorActor(), SearchInput{Query: "CB.SC.U4CSE99001"}); err != nil {
		t.Fatalf("Search = %v, want nil", err)
	}
	got := capture.queries[0]

	if got.ExactRoll == "" {
		t.Error("[P0] the query carries no exact-roll term — an exact roll match must sort first")
	}
	if got.ExactRoll != strings.ToUpper(strings.TrimSpace("CB.SC.U4CSE99001")) {
		t.Errorf("[P0] ExactRoll = %q, want the canonical uppercase roll (roll_no is stored uppercase)", got.ExactRoll)
	}
}

// 0.6-SEARCH-001f — the result shape carries nothing private. This is a
// structural guard: the field simply must not exist to be populated.
//
// It reflects over the struct rather than naming fields. The earlier version
// declared `result := SearchResult{}` and then `_ = result` under a comment
// claiming "if any of these compile, the field exists" — there were no field
// references, so adding `Email string` left it green. A guard that cannot fail
// is worse than no guard: it reads as coverage.
func TestSearchResult_CarriesNoPrivateFields(t *testing.T) {
	allowed := map[string]bool{
		"StudentID": true, "RollNo": true, "DisplayName": true,
		"Branch": true, "Section": true, "Batch": true,
	}
	banned := []string{"email", "token", "fingerprint", "secret", "hmac", "seal"}

	shape := reflect.TypeOf(SearchResult{})
	for i := range shape.NumField() {
		name := shape.Field(i).Name
		if !allowed[name] {
			t.Errorf("[P0] SearchResult gained field %q — this shape is the roster search's wire contract; "+
				"a new field here is exposed to every coordinator. Add it to `allowed` only if it is public roster data.", name)
		}
		for _, forbidden := range banned {
			if strings.Contains(strings.ToLower(name), forbidden) {
				t.Errorf("[P0] SearchResult.%s matches the forbidden term %q — students has no email column "+
					"and card tokens are fingerprint-only; a field that does not exist cannot be populated later.", name, forbidden)
			}
		}
	}

	capture := &searchCapture{results: []SearchResult{{
		StudentID:   "55555555-5555-4555-8555-555555555555",
		RollNo:      "CB.SC.U4CSE99001",
		DisplayName: "Scaffold Student",
	}}}
	svc := newSearchService(capture)

	out, err := svc.Search(context.Background(), coordinatorActor(), SearchInput{Query: "CB.SC.U4CSE99001"})
	if err != nil {
		t.Fatalf("Search = %v, want nil", err)
	}
	if len(out) != 1 {
		t.Fatalf("[P0] got %d results, want 1", len(out))
	}
	if out[0].RollNo != "CB.SC.U4CSE99001" || out[0].DisplayName != "Scaffold Student" {
		t.Errorf("[P0] result = %+v, want the roll and display name the coordinator picks by", out[0])
	}
}
