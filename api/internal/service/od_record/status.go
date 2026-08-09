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

// CountReader returns (mine, categoryTotal) for one actor, scope and DAY.
type CountReader interface {
	CountMarks(ctx context.Context, actorUserID, eventID, categoryID string, odDate time.Time) (int, int, error)
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
	// slotCeiling is optional; nil means the standard teaching day. See
	// Service.WithSlotCeiling for why it is a setter rather than a parameter.
	slotCeiling SlotCeiling
}

// WithSlotCeiling opts the status read into the extended-slots switch, so the
// countdown a coordinator sees matches the slot the mark path will accept. The
// two reading different ceilings would show an open slot that then refuses the
// scan.
func (s *StatusService) WithSlotCeiling(ceiling SlotCeiling) *StatusService {
	s.slotCeiling = ceiling
	return s
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

	// One clock read serves server_time, the slot decision, the day the counters
	// are scoped to and the day the credit pool is read for. Separate reads could
	// disagree across midnight, which would show a coordinator one day's counters
	// beside another day's remaining credits.
	now := s.clock.Now()
	today := CampusDate(now)

	status := Status{ServerTime: now}
	ceiling := StandardSlotCount
	if s.slotCeiling != nil {
		extended, err := s.slotCeiling.ExtendedSlotsEnabled(ctx)
		if err != nil {
			return Status{}, err
		}
		ceiling = SlotCount(extended)
	}
	if _, window, open := ActiveSlot(now, event.EventDate, event.EndDate, ceiling); open {
		slot := window
		status.Slot = &slot
	}

	mine, categoryTotal, err := s.counts.CountMarks(ctx, actor.UserID, eventID, categoryID, today)
	if err != nil {
		return Status{}, err
	}
	status.Counters = Counters{Mine: mine, CategoryTotal: categoryTotal}
	credit, err := s.credits.State(ctx, eventID, actor.UserID, today)
	if err != nil {
		return Status{}, err
	}
	status.FallbackCredit = credit
	return status, nil
}
