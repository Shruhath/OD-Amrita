package student

import "context"

// PublicSearchLimit is the roster-search result cap for the UNAUTHENTICATED
// public dashboard (Story 0.10).
//
// Lower than the authenticated search's defaultSearchLimit (20) and
// MaxSearchLimit (50), and — unlike those — not adjustable by the caller: the
// public endpoint accepts no `limit` parameter at all. A knob the open internet
// controls is a knob that gets turned to 10,000, and the exact-roll-first
// ordering buildSearchQuery already applies means a student who types their
// full roll number still lands on themselves at position one.
const PublicSearchLimit = 10

// PublicSearch prefix-matches the roster for a caller with no credential.
//
// It takes NO actor, deliberately. There is no identity to authorize here, so
// there is no parameter that could tempt a later change into inventing an
// "anonymous" role and relaxing assertMaySearch to admit it — which would make
// the authenticated roster readable by every signed-in student, a far larger
// disclosure than this bounded, capped, display-only view.
//
// It reuses buildSearchQuery rather than building a pattern of its own. The
// LIKE escaping and the rune bounds are the security-critical part: unescaped,
// a single `%` matches the entire roster, which is precisely the dump the
// minimum-length guard exists to prevent — now reachable by anyone with a
// browser. A second copy of that logic is a second thing to get wrong.
func (s *Service) PublicSearch(ctx context.Context, query string) ([]SearchResult, error) {
	// Bound BEFORE the reader is consulted: the point of the minimum length is
	// that the database is never asked. A service that queries and then discards
	// has already paid the cost the bound exists to avoid.
	bounded, err := buildSearchQuery(SearchInput{Query: query, Limit: PublicSearchLimit})
	if err != nil {
		return nil, err
	}
	if s.searchReader == nil {
		return nil, errSearchReaderMissing
	}
	return s.searchReader.SearchStudents(ctx, bounded)
}
