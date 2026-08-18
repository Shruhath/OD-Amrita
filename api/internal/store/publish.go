package store

// publish.go — the bridge from the audit write boundary to the domain event
// stream.
//
// WHY THE STREAM IS ATTACHED HERE AND NOWHERE ELSE
//
//	Every state change in this system already passes through WithAudit with a
//	registered domain.Action — that is the invariant tx.go exists to enforce, and
//	it is enforced at runtime by the registry gate. So this one seam gives the
//	stream complete coverage: every mark, every archive, every assignment, every
//	login, without a publish call being added to (and later forgotten in) fifteen
//	services. A publish sprinkled per service would drift the first time somebody
//	added an endpoint; this cannot, because a write that skipped this seam would
//	not have an audit row either, and that is already impossible.

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Shruhath/OD-Amrita/api/internal/events"
)

// WithPublisher attaches the domain event stream.
//
// Omitted — which is how every existing construction and every test builds a
// Store — the publisher is events.Nop and nothing about the write path changes.
func WithPublisher(publisher events.Publisher) Option {
	return func(s *Store) {
		if publisher != nil {
			s.publisher = publisher
		}
	}
}

// publish mirrors a committed audit event onto the stream.
//
// THE THREE RULES THIS FUNCTION ENCODES, ALL OF WHICH ARE LOAD-BEARING:
//
//  1. AFTER COMMIT, NEVER INSIDE THE TRANSACTION. A produce inside the tx would
//     hold a Postgres transaction open across a network round trip to a broker.
//     On Neon that is billed, awake, connection-holding time added to every
//     write in the system — the precise cost this codebase spent a release
//     removing — and a slow broker would become long-running transactions and
//     lock contention on the marking path.
//
//  2. ONLY WHEN THE WRITE ACTUALLY CHANGED SOMETHING. It is called from the same
//     branch that inserts the audit row, so a dedup no-op that commits without an
//     audit row publishes nothing either. The stream and the ledger agree on what
//     counted as an event, because they are decided by the same `if`.
//
//  3. IT CANNOT FAIL THE WRITE. Publish returns nothing (see events.Publisher),
//     so there is no error here to accidentally propagate. The write has already
//     committed; the student is already marked.
func (s *Store) publish(ctx context.Context, evt AuditEvent) {
	if s.publisher == nil {
		return
	}
	envelope := events.Envelope{
		ID:            uuid.NewString(),
		Type:          string(evt.Action),
		SchemaVersion: events.SchemaVersion,
		OccurredAt:    time.Now().UTC(),
		ActorAccount:  deref(evt.ActorAccount),
		ActorRole:     deref(evt.ActorRole),
		TargetTable:   deref(evt.TargetTable),
		TargetID:      deref(evt.TargetID),
		Payload:       evt.Payload,
	}
	// The request's context is deliberately NOT passed on to the buffer. By the
	// time this runs the response is about to be written, and a caller that
	// disconnects the instant their mark commits would otherwise cancel the
	// notification of a fact that definitely happened. AsyncPublisher does not
	// block, so there is nothing here that needs cancelling.
	s.publisher.Publish(context.WithoutCancel(ctx), envelope)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
