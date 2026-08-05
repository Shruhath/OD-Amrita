package student

import (
	"context"
	"strings"
	"unicode/utf8"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

const (
	// MinSearchRunes is judged AFTER trimming. Three spaces is not a query, and an
	// unbounded one would be a roster dump of 1,534 real people.
	//
	// Exported so the HTTP edge can reject a too-short query without a pointless
	// service round trip while still using THIS number. The service checks it
	// again regardless: a non-HTTP caller must not be able to skip the bound.
	MinSearchRunes = 3
	MaxSearchRunes = 64

	defaultSearchLimit = 20
	// MaxSearchLimit is exported for the same reason MinSearchRunes is: the HTTP
	// edge bounds `limit` too, and when the cap lived in two places the two
	// disagreed in behaviour — the service clamped 10,000 down to 50 while the
	// handler rejected 51 outright, so raising one silently changed nothing.
	// There is now ONE number.
	MaxSearchLimit = 50
)

// SearchInput is what the caller asked for. What the DATABASE is asked for is
// SearchQuery, and the gap between the two is where the bounding happens.
type SearchInput struct {
	Query string
	Limit int
}

// SearchQuery is the bounded, escaped question the reader actually runs.
type SearchQuery struct {
	// Pattern is a prefix pattern: escaped user input followed by a single
	// trailing `%`. It never begins with a wildcard.
	Pattern string
	// ExactRoll is the canonical uppercase roll used to sort an exact match first.
	ExactRoll string
	// Limit is already clamped to 1..MaxSearchLimit.
	Limit int
}

// SearchResult carries display fields only. There is deliberately no email,
// token or fingerprint field: `students` has no email column and card tokens are
// stored as fingerprints, and a field that does not exist cannot be populated by
// a later convenience change.
type SearchResult struct {
	StudentID   string
	RollNo      string
	DisplayName string
	Branch      string
	Section     string
	Batch       string
}

type SearchReader interface {
	SearchStudents(context.Context, SearchQuery) ([]SearchResult, error)
}

// WithSearchReader attaches the roster search reader.
//
// It is an add-on rather than a fourth NewService parameter so the admin roster
// import and list paths — which have no business searching — keep constructing
// the service exactly as they did.
func (s *Service) WithSearchReader(reader SearchReader) *Service {
	s.searchReader = reader
	return s
}

// Search prefix-matches the roster by roll number or name.
//
// Ordering is exact-roll-first: a coordinator who types a full roll number is
// looking at one person and must not have to scan a list for them. `id` is the
// final tiebreaker so a repeated search cannot reorder under their finger.
func (s *Service) Search(ctx context.Context, actor Actor, input SearchInput) ([]SearchResult, error) {
	if err := assertMaySearch(actor); err != nil {
		return nil, err
	}
	query, err := buildSearchQuery(input)
	if err != nil {
		return nil, err
	}
	if s.searchReader == nil {
		return nil, errSearchReaderMissing
	}
	return s.searchReader.SearchStudents(ctx, query)
}

// assertMaySearch keeps the roster behind a role that has a reason to read it.
// Students may never search; the marking scope seam narrows further still for
// the marking screen itself.
func assertMaySearch(actor Actor) error {
	switch actor.Role {
	case "coordinator", "teacher", "admin":
		return nil
	default:
		return domain.DomainError{
			Code:    contracts.ErrorCodeRBACFORBIDDEN,
			Message: "Coordinator, teacher, or admin role required.",
		}
	}
}

func buildSearchQuery(input SearchInput) (SearchQuery, error) {
	trimmed := strings.TrimSpace(input.Query)
	if utf8.RuneCountInString(trimmed) < MinSearchRunes {
		return SearchQuery{}, searchValidation("q", "min_length", "Search needs at least 3 characters.")
	}
	if utf8.RuneCountInString(trimmed) > MaxSearchRunes {
		return SearchQuery{}, searchValidation("q", "max_length", "Search must not exceed 64 characters.")
	}
	// A NUL byte is valid UTF-8, so it survives TrimSpace, the rune counts and
	// escapeLikePattern (which only special-cases \ % _) and reaches Postgres as
	// a bind parameter — where it is rejected with SQLSTATE 22021, "invalid byte
	// sequence for encoding UTF8". That surfaces as a 500 on an endpoint anyone
	// can call, for a six-character query string. It is a malformed input, so it
	// gets the typed 400 every other malformed input here gets.
	//
	// The check is the whole C0 range rather than NUL alone: no control
	// character belongs in a roll number or a name, none of them can match a
	// row, and enumerating just the one that happens to error today invites the
	// next one to be found in production.
	if strings.IndexFunc(trimmed, isControl) >= 0 {
		return SearchQuery{}, searchValidation("q", "invalid_characters", "Search must not contain control characters.")
	}
	return SearchQuery{
		Pattern:   escapeLikePattern(trimmed) + "%",
		ExactRoll: strings.ToUpper(trimmed),
		Limit:     boundSearchLimit(input.Limit),
	}, nil
}

// isControl reports whether r is a C0/C1 control character or DEL.
func isControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f)
}

// boundSearchLimit is the server's answer, not the client's ask. A request for
// 10,000 rows gets the cap.
func boundSearchLimit(requested int) int {
	if requested <= 0 {
		return defaultSearchLimit
	}
	if requested > MaxSearchLimit {
		return MaxSearchLimit
	}
	return requested
}

// escapeLikePattern neutralises LIKE's metacharacters in user input. Unescaped,
// `_` matches any single character and `%` matches the entire roster — which is
// exactly the dump the minimum-length guard exists to prevent. The backslash
// itself is escaped first, or escaping the other two would be reversible.
func escapeLikePattern(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 8)
	for _, r := range value {
		switch r {
		case '\\', '%', '_':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func searchValidation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code:    contracts.ErrorCodeVALIDATIONFAILED,
		Message: "Student search validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}
