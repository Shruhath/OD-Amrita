package store

// publish_test.go — UNIT tests for the event-stream bridge. No database: the
// pool is only reached by WithAudit itself, and everything decided in this file
// — what an envelope carries, and when a held event is published or discarded —
// is decided in pure Go. The commit/rollback ORDERING is proven against a real
// transaction in the integration suite, which is the only place it can be.
//
// Invoked by: api-ci -> build job.

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/events"
)

// fakeTx satisfies pgx.Tx by embedding the nil interface. Nothing here calls a
// method on it — it is used only as a map key, which is exactly what the pending
// registry uses a transaction for.
type fakeTx struct{ pgx.Tx }

type capturingPublisher struct {
	mu        sync.Mutex
	published []events.Envelope
}

func (p *capturingPublisher) Publish(_ context.Context, envelope events.Envelope) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = append(p.published, envelope)
}

func (p *capturingPublisher) all() []events.Envelope {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]events.Envelope, len(p.published))
	copy(out, p.published)
	return out
}

func newTestStore(publisher events.Publisher) *Store {
	return &Store{
		auditInserter: defaultAuditInserter,
		publisher:     publisher,
		pending:       make(map[pgx.Tx][]AuditEvent),
	}
}

// TestPublish_CarriesTheAuditFieldsVerbatim — [P1].
//
// A consumer and an auditor reading the same event must see the same facts. The
// payload especially: it is handed on as the SAME bytes the ledger stored, not
// re-marshalled from a parallel struct that could drift from it.
func TestPublish_CarriesTheAuditFieldsVerbatim(t *testing.T) {
	publisher := &capturingPublisher{}
	store := newTestStore(publisher)

	account, role := "coord@amrita.edu", "coordinator"
	table, id := "od_records", "record-1"
	store.publish(context.Background(), AuditEvent{
		Action:       domain.ActionODCreated,
		ActorAccount: &account,
		ActorRole:    &role,
		TargetTable:  &table,
		TargetID:     &id,
		Payload:      []byte(`{"slot":3}`),
	})

	published := publisher.all()
	if len(published) != 1 {
		t.Fatalf("[P1] published %d envelopes, want 1", len(published))
	}
	got := published[0]
	if got.Type != string(domain.ActionODCreated) {
		t.Errorf("[P1] Type = %q, want %q", got.Type, domain.ActionODCreated)
	}
	if got.ActorAccount != account || got.ActorRole != role {
		t.Errorf("[P1] actor = %q/%q, want %q/%q", got.ActorAccount, got.ActorRole, account, role)
	}
	if got.TargetTable != table || got.TargetID != id {
		t.Errorf("[P1] target = %q/%q, want %q/%q", got.TargetTable, got.TargetID, table, id)
	}
	if string(got.Payload) != `{"slot":3}` {
		t.Errorf("[P1] Payload = %s, want the ledger's own bytes", got.Payload)
	}
	if got.ID == "" {
		t.Error("[P1] every publish needs its own id")
	}
	if got.OccurredAt.IsZero() {
		t.Error("[P1] OccurredAt must be stamped")
	}
}

// TestPublish_NilOptionalsBecomeEmptyStrings — [P2]. The ledger distinguishes
// "no account" (SQL NULL) from an empty one; on the wire, omitempty collapses
// both, and a consumer must not see the literal string "<nil>".
func TestPublish_NilOptionalsBecomeEmptyStrings(t *testing.T) {
	publisher := &capturingPublisher{}
	store := newTestStore(publisher)

	store.publish(context.Background(), AuditEvent{Action: domain.ActionAuthDomainRejected})

	got := publisher.all()[0]
	if got.ActorAccount != "" || got.ActorRole != "" || got.TargetTable != "" || got.TargetID != "" {
		t.Errorf("[P2] nil optionals must render empty, got %+v", got)
	}
}

// TestPublish_IsInertWithoutAPublisher — [P0]. This is the default construction
// and the state production runs in: no publisher attached, nothing happens, and
// crucially no nil dereference on the marking path.
func TestPublish_IsInertWithoutAPublisher(t *testing.T) {
	store := &Store{auditInserter: defaultAuditInserter}
	store.publish(context.Background(), AuditEvent{Action: domain.ActionODCreated})
}

// TestNew_DefaultsToTheNopPublisher — [P0]. Every existing construction in the
// codebase and every test calls store.New(pool) with no options. If that left
// publisher nil, publish would still be safe — but Nop is what makes "the write
// path is unchanged" true by construction rather than by a nil check.
func TestNew_DefaultsToTheNopPublisher(t *testing.T) {
	store := New(nil)
	if _, ok := store.publisher.(events.Nop); !ok {
		t.Errorf("[P0] default publisher = %T, want events.Nop", store.publisher)
	}
}

// TestWithPublisher_IgnoresNil — [P2]. A caller that passes a nil Publisher
// meant "off", and must get the Nop rather than a nil field.
func TestWithPublisher_IgnoresNil(t *testing.T) {
	store := New(nil, WithPublisher(nil))
	if store.publisher == nil {
		t.Fatal("[P2] WithPublisher(nil) must leave the Nop in place")
	}
}

// TestPending_HeldEventsAreScopedToTheirTransaction — [P0].
//
// Two coordinators marking at once run two transactions through the same Store.
// A single shared slice would publish one coordinator's extra events under the
// other's commit — and, worse, publish events belonging to a transaction that
// rolled back.
func TestPending_HeldEventsAreScopedToTheirTransaction(t *testing.T) {
	store := newTestStore(&capturingPublisher{})
	txA, txB := &fakeTx{}, &fakeTx{}

	store.holdPending(txA, AuditEvent{Action: domain.ActionODCreated})
	store.holdPending(txB, AuditEvent{Action: domain.ActionDisputeResolved})

	heldA := store.takePending(txA)
	if len(heldA) != 1 || heldA[0].Action != domain.ActionODCreated {
		t.Fatalf("[P0] transaction A held %+v, want only its own event", heldA)
	}
	heldB := store.takePending(txB)
	if len(heldB) != 1 || heldB[0].Action != domain.ActionDisputeResolved {
		t.Fatalf("[P0] transaction B held %+v, want only its own event", heldB)
	}
}

// TestPending_TakeIsDestructive — [P1]. WithAudit publishes what it takes; if
// take left the entry behind, a later transaction reusing the same pooled
// connection object would republish it.
func TestPending_TakeIsDestructive(t *testing.T) {
	store := newTestStore(&capturingPublisher{})
	tx := &fakeTx{}

	store.holdPending(tx, AuditEvent{Action: domain.ActionODCreated})
	_ = store.takePending(tx)

	if again := store.takePending(tx); len(again) != 0 {
		t.Errorf("[P1] a second take returned %d events, want 0", len(again))
	}
	if len(store.pending) != 0 {
		t.Errorf("[P1] the pending map still holds %d transactions — this is a slow leak", len(store.pending))
	}
}

// TestPending_DropDiscardsWithoutPublishing — [P0].
//
// dropPending runs on EVERY exit from WithAudit. On a rollback the held events
// describe writes that never happened, and publishing them would tell the rest
// of the system a student was marked when the transaction was undone.
func TestPending_DropDiscardsWithoutPublishing(t *testing.T) {
	publisher := &capturingPublisher{}
	store := newTestStore(publisher)
	tx := &fakeTx{}

	store.holdPending(tx, AuditEvent{Action: domain.ActionODCreated})
	store.dropPending(tx)

	if len(publisher.all()) != 0 {
		t.Error("[P0] dropPending must not publish")
	}
	if len(store.takePending(tx)) != 0 {
		t.Error("[P0] dropped events must not survive to a later take")
	}
	if len(store.pending) != 0 {
		t.Errorf("[P0] pending map retained %d entries after a drop", len(store.pending))
	}
}
