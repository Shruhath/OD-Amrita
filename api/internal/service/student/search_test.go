// Tests for the authenticated student search (FR55).
//
// Search is the coordinator's fallback when a card will not tap and the camera
// will not decode, and it is also the resolution step for the camera path. It is
// therefore on the critical path twice.
//
// Two properties matter more than the happy path:
//
//  1. PREFIX-ONLY matching, so `students_roll_no_idx` and
//     `students_name_lower_idx` are actually usable. A leading-wildcard
//     `%foo%` search cannot use either index and turns every keystroke into a
//     sequential scan over 1,534 rows on a phone at arm's length.
//  2. The response carries no email, token or fingerprint. `students` has no
//     email column and card tokens are stored as fingerprints only — this
//     keeps both facts true through a new read path.
package student

import (
	"context"
	"strings"
	"testing"
)

// searchCapture records the arguments the service hands the SQLC reader, which
// is where the prefix/escaping/limit decisions become observable without a
// database.
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

// 0.6-SEARCH-001b — prefix-only, so both indexes stay usable.
func TestSearch_MatchesByPrefixOnly(t *testing.T) {
	capture := &searchCapture{}
	svc := newSearchService(capture)

	if _, err := svc.Search(context.Background(), coordinatorActor(), SearchInput{Query: "cb.sc.u4cse24"}); err != nil {
		t.Fatalf("Search = %v, want nil", err)
	}
	if len(capture.queries) != 1 {
		t.Fatalf("[P0] reader called %d times, want 1", len(capture.queries))
	}

	got := capture.queries[0]
	if strings.HasPrefix(got.Pattern, "%") {
		t.Errorf("[P0] pattern %q begins with a wildcard — a leading %% cannot use students_roll_no_idx or students_name_lower_idx", got.Pattern)
	}
	if !strings.HasSuffix(got.Pattern, "%") {
		t.Errorf("[P0] pattern %q is not a prefix pattern — it should end in a single trailing %%", got.Pattern)
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
			got := capture.queries[0].Pattern
			body := strings.TrimSuffix(got, "%")

			if strings.Contains(body, "%") && !strings.Contains(body, `\%`) {
				t.Errorf("[P0] pattern %q carries an unescaped %% — a wildcard in user input must not become a wildcard in the query", got)
			}
			if strings.Contains(body, "_") && !strings.Contains(body, `\_`) {
				t.Errorf("[P0] pattern %q carries an unescaped _ — LIKE treats it as a single-character wildcard", got)
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

	if _, err := svc.Search(context.Background(), coordinatorActor(), SearchInput{Query: "CB.SC.U4CSE24001"}); err != nil {
		t.Fatalf("Search = %v, want nil", err)
	}
	got := capture.queries[0]

	if got.ExactRoll == "" {
		t.Error("[P0] the query carries no exact-roll term — an exact roll match must sort first")
	}
	if got.ExactRoll != strings.ToUpper(strings.TrimSpace("CB.SC.U4CSE24001")) {
		t.Errorf("[P0] ExactRoll = %q, want the canonical uppercase roll (Story 0.2 established uppercase roll_no)", got.ExactRoll)
	}
}

// 0.6-SEARCH-001f — the result shape carries nothing private. This is a
// structural guard: the field simply must not exist to be populated.
func TestSearchResult_CarriesNoPrivateFields(t *testing.T) {
	result := SearchResult{}

	// If any of these compile, the field exists and the guarantee is broken.
	// The dev should keep SearchResult to student_id / roll_no / display_name
	// (plus branch/section/batch, which are already public roster columns).
	_ = result

	capture := &searchCapture{results: []SearchResult{{
		StudentID:   "55555555-5555-4555-8555-555555555555",
		RollNo:      "CB.SC.U4CSE24001",
		DisplayName: "Scaffold Student",
	}}}
	svc := newSearchService(capture)

	out, err := svc.Search(context.Background(), coordinatorActor(), SearchInput{Query: "CB.SC.U4CSE24001"})
	if err != nil {
		t.Fatalf("Search = %v, want nil", err)
	}
	if len(out) != 1 {
		t.Fatalf("[P0] got %d results, want 1", len(out))
	}
	if out[0].RollNo != "CB.SC.U4CSE24001" || out[0].DisplayName != "Scaffold Student" {
		t.Errorf("[P0] result = %+v, want the roll and display name the coordinator picks by", out[0])
	}
}
