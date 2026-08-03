package odrecord

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Counters are the two FR13 figures. Both are server-computed: the client never
// counts a mark itself, or the number on screen would disagree with the audit
// trail the moment a request failed.
type Counters struct {
	// Mine is the calling coordinator's marks for this event and category.
	Mine int
	// CategoryTotal is every coordinator's, and is identical for all callers.
	CategoryTotal int
}

// Status is one snapshot of the marking screen's server-owned state.
type Status struct {
	// ServerTime comes from the SAME clock read that decided Slot. If the two
	// came from separate reads, the coordinator's countdown and the server's
	// slot decision could disagree by a poll interval — which shows up as "the
	// timer said twelve seconds left but it rejected my tap".
	ServerTime time.Time
	// Slot is nil when no slot is open. That absence is the state (FR14); slot 0
	// is not a slot and must never be used to encode it.
	Slot           *SlotWindow
	Counters       Counters
	FallbackCredit FallbackCreditState
}

// CountReader returns (mine, categoryTotal) for one actor and scope.
type CountReader interface {
	CountMarks(ctx context.Context, actorUserID, eventID, categoryID string) (int, int, error)
}

// StatusService answers GET /api/v1/mark/status.
//
// It holds no scope policy of its own: the handler runs the marking scope seam
// before calling it, exactly as the mark write does.
type StatusService struct {
	reader  Reader
	counts  CountReader
	credits CreditLedger
	clock   Clock
}

func NewStatusService(reader Reader, counts CountReader, credits CreditLedger, clock Clock) *StatusService {
	if clock == nil {
		clock = systemClock{}
	}
	if credits == nil {
		panic("od_record: NewStatusService requires a CreditLedger")
	}
	return &StatusService{reader: reader, counts: counts, credits: credits, clock: clock}
}

func (s *StatusService) MarkStatus(ctx context.Context, actor Actor, eventID, categoryID string) (Status, error) {
	if _, err := uuid.Parse(strings.TrimSpace(eventID)); err != nil {
		return Status{}, validation("event_id", "invalid_format", "event_id must be a UUID.")
	}
	if _, err := uuid.Parse(strings.TrimSpace(categoryID)); err != nil {
		return Status{}, validation("category_id", "invalid_format", "category_id must be a UUID.")
	}

	event, err := s.reader.EventForMark(ctx, eventID)
	if errors.Is(err, ErrEventNotFound) {
		return Status{}, eventNotFound()
	}
	if err != nil {
		return Status{}, err
	}

	// One clock read serves both the slot decision and server_time.
	now := s.clock.Now()

	status := Status{ServerTime: now}
	if _, window, open := ActiveSlot(now, event.EventDate); open {
		slot := window
		status.Slot = &slot
	}

	mine, categoryTotal, err := s.counts.CountMarks(ctx, actor.UserID, eventID, categoryID)
	if err != nil {
		return Status{}, err
	}
	status.Counters = Counters{Mine: mine, CategoryTotal: categoryTotal}
	credit, err := s.credits.State(ctx, eventID, actor.UserID)
	if err != nil {
		return Status{}, err
	}
	status.FallbackCredit = credit
	return status, nil
}
