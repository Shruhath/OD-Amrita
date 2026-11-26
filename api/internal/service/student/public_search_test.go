// Tests for the UNAUTHENTICATED roster search.
//
// This is the same roster the coordinator surface searches, answered to anyone
// on the internet with no token. Three properties carry that difference:
//
//  1. It takes NO actor. There is no identity to authorize, so there must be no
//     parameter that could tempt someone into inventing an "anonymous" role and
//     relaxing assertMaySearch to admit it.
//  2. The result cap is FIXED at 10, server-side. The authenticated search has a
//     caller-supplied `limit`; a knob the open internet controls is a knob that
//     gets turned to 10,000 (T7).
//  3. It reuses buildSearchQuery. The LIKE escaping and the rune bounds are the
//     security-critical part, and a second copy is a second thing to get wrong.
//
// NOT gated behind an env flag: 0.9 shipped a suite behind ATDD_ACTIVE_09 that
// nothing ever set.
package student

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// assertOnlyAllowedFields fails if the value carries any exported field outside
// the allowlist.
//
// An ALLOWLIST, not a blocklist. An earlier equivalent assertion listed the
// sensitive names it did not want and could therefore never fail — a struct that
// never had an Email field passes trivially, and the field somebody adds next
// year is not on the list either.
func assertOnlyAllowedFields(t *testing.T, value any, allowed map[string]bool) {
	t.Helper()
	typ := reflect.TypeOf(value)
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if !typ.Field(i).IsExported() {
			continue
		}
		if !allowed[name] {
			t.Errorf("[P0] %s carries field %q, which is not on the public allowlist. This value is "+
				"serialised to an UNAUTHENTICATED response: adding a field here is a disclosure decision, "+
				"so add it to the allowlist in the same change and say why", typ.Name(), name)
		}
	}
}

// 0.10-SEARCH-001 — the cap is the SERVER's answer, always, and it is 10.
//
// There is no `limit` parameter on the public endpoint (contract test 002), so
// the only way this can be wrong is a service that defaults to the authenticated
// search's 20 or clamps to its 50.
func TestPublicSearch_AlwaysCapsAtTen(t *testing.T) {
	if PublicSearchLimit != 10 {
		t.Errorf("[P0] PublicSearchLimit = %d, want 10", PublicSearchLimit)
	}

	capture := &searchCapture{}
	svc := newSearchService(capture)

	if _, err := svc.PublicSearch(context.Background(), "CB.SC.U4CSE99"); err != nil {
		t.Fatalf("[P0] PublicSearch on a valid query failed: %v", err)
	}
	if len(capture.queries) != 1 {
		t.Fatalf("[P0] PublicSearch ran %d roster queries, want 1", len(capture.queries))
	}
	if got := capture.queries[0].Limit; got != PublicSearchLimit {
		t.Errorf("[P0] public query limit = %d, want %d. The public search must never inherit the "+
			"authenticated defaultSearchLimit (20) or MaxSearchLimit (50) — those are bounds for a "+
			"signed-in coordinator, not for the open internet", got, PublicSearchLimit)
	}
}

// 0.10-SEARCH-002 — the rune bounds are enforced, and NO roster query runs when
// they are not met (AC 2).
//
// "Returns an error" is not enough: the point of the minimum length is that the
// database is never asked. A service that queries and then discards has already
// paid the cost the bound exists to avoid.
func TestPublicSearch_EnforcesRuneBoundsBeforeQuerying(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
	}{
		{"empty", ""},
		{"blank", "   "},
		{"two runes", "ab"},
		{"two runes after trimming", "  ab  "},
		{"tabs and newlines", "\t\n"},
		{"over 64 runes", strings.Repeat("a", MaxSearchRunes+1)},
		// A NUL byte is valid UTF-8, so it clears TrimSpace, both rune counts
		// and escapeLikePattern, then reaches Postgres and is rejected there
		// with SQLSTATE 22021 — a 500 on an endpoint with no credential, caused
		// by six characters of query string. It must be refused here, before the
		// reader is consulted, like every other malformed input.
		{"nul byte", "CB.\x00"},
		{"interior control character", "CB\x01SC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := &searchCapture{}
			svc := newSearchService(capture)

			if _, err := svc.PublicSearch(context.Background(), tc.query); err == nil {
				t.Fatalf("[P0] PublicSearch(%q) succeeded; the bound must hold for an anonymous caller "+
					"exactly as it does for a coordinator", tc.query)
			}
			if len(capture.queries) != 0 {
				t.Errorf("[P0] PublicSearch(%q) ran %d roster queries; an out-of-bounds query must be "+
					"refused BEFORE the database is asked", tc.query, len(capture.queries))
			}
		})
	}

	// The bounds are the EXPORTED constants, not a second copy. If someone
	// changes MinSearchRunes, this test must follow automatically.
	capture := &searchCapture{}
	svc := newSearchService(capture)
	if _, err := svc.PublicSearch(context.Background(), strings.Repeat("a", MinSearchRunes)); err != nil {
		t.Errorf("[P0] a query of exactly MinSearchRunes (%d) was refused: %v", MinSearchRunes, err)
	}
	if _, err := svc.PublicSearch(context.Background(), strings.Repeat("a", MaxSearchRunes)); err != nil {
		t.Errorf("[P0] a query of exactly MaxSearchRunes (%d) was refused: %v", MaxSearchRunes, err)
	}
}

// 0.10-SEARCH-003 — LIKE metacharacters are escaped, per word.
//
// Unescaped, `%` matches the entire roster — which is precisely the dump the
// minimum-length guard exists to prevent, now reachable by anyone with a browser.
// The backslash must be escaped first or escaping the other two is reversible.
func TestPublicSearch_EscapesLikeMetacharacters(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want []string
	}{
		{"100%", []string{`%100\%%`}},
		{"a_b", []string{`%a\_b%`}},
		{`a\b`, []string{`%a\\b%`}},
		{`%_\`, []string{`%\%\_\\%`}},
		{"CB.SC.U4CSE99", []string{`%cb.sc.u4cse99%`}},
		// Split on whitespace, so each word is escaped on its own — the public
		// path gets the same "any word, any order" matching as the staff one.
		{"Reddy Shruhat", []string{`%reddy%`, `%shruhat%`}},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			capture := &searchCapture{}
			svc := newSearchService(capture)

			if _, err := svc.PublicSearch(context.Background(), tc.raw); err != nil {
				t.Fatalf("PublicSearch(%q): %v", tc.raw, err)
			}
			if len(capture.queries) != 1 {
				t.Fatalf("[P0] ran %d queries, want 1", len(capture.queries))
			}
			patterns := capture.queries[0].Patterns
			if !reflect.DeepEqual(patterns, tc.want) {
				t.Errorf("[P0] patterns = %v, want %v — reuse buildSearchQuery rather than building the "+
					"patterns again in the public path", patterns, tc.want)
			}
			// The escaping is the security-critical half and it is why this test
			// exists at all: an unescaped `%` reaching the query makes a
			// three-character anonymous request return the whole roster, which is
			// exactly the dump the minimum-length guard is there to prevent.
			for _, pattern := range patterns {
				body := strings.TrimSuffix(strings.TrimPrefix(pattern, "%"), "%")
				if strings.Contains(body, "%") && !strings.Contains(body, `\%`) {
					t.Errorf("[P0] pattern %q carries an unescaped %%", pattern)
				}
			}
		})
	}
}

// 0.10-SEARCH-004 — the authenticated path is UNCHANGED.
//
// The tempting shortcut for the public search is to relax assertMaySearch or add an
// "anonymous" actor so the existing Search can be reused. That would make the
// authenticated roster readable by any signed-in student, which is a much larger
// disclosure than the public endpoint's own bounded, capped, display-only view.
func TestSearch_StillRefusesAStudentRoleAfterThePublicPathExists(t *testing.T) {
	for _, role := range []string{"student", "", "guest", "public", "anonymous"} {
		t.Run("role="+role, func(t *testing.T) {
			capture := &searchCapture{}
			svc := newSearchService(capture)

			_, err := svc.Search(context.Background(), Actor{UserID: "u-1", Role: role}, SearchInput{Query: "CB.SC.U4CSE99"})
			if err == nil {
				t.Fatalf("[P0] the AUTHENTICATED Search admitted role %q. The public read is a separate, "+
					"capped, actor-less path — it must never be delivered by widening this one", role)
			}
			if len(capture.queries) != 0 {
				t.Errorf("[P0] a refused actor still reached the roster query")
			}
		})
	}
}

// 0.10-SEARCH-005 — the public read returns display fields only.
//
// SearchResult has no email/token/fingerprint field and SearchStudents
// joins no card table, so this holds structurally today. The test states the
// invariant so that a later "convenience" widening of SearchResult fails here
// rather than on the open internet.
func TestPublicSearch_ReturnsDisplayFieldsOnly(t *testing.T) {
	capture := &searchCapture{results: []SearchResult{{
		StudentID:   "95000000-0000-4000-8000-000000000001",
		RollNo:      "CB.SC.U4CSE99001",
		DisplayName: "Ananya Rao",
		Branch:      "CSE",
		Section:     "A",
		Batch:       "2024",
	}}}
	svc := newSearchService(capture)

	results, err := svc.PublicSearch(context.Background(), "CB.SC.U4CSE99001")
	if err != nil {
		t.Fatalf("PublicSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("[P0] got %d results, want 1", len(results))
	}

	// Reflection over the struct is the assertion that survives someone ADDING a
	// field: an explicit field-by-field check only ever sees the fields the test
	// author already knew about.
	allowed := map[string]bool{
		"StudentID": true, "RollNo": true, "DisplayName": true, "Branch": true, "Section": true, "Batch": true,
	}
	assertOnlyAllowedFields(t, results[0], allowed)
}
