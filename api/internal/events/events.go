// Package events is the domain event stream: a transport-free description of
// "something happened", plus the seam that carries it off this process.
//
// WHY THIS EXISTS AT ALL, GIVEN THE AUDIT LEDGER ALREADY RECORDS EVERYTHING
//
//	audit_log answers "what happened?" for a human reading history. It is a
//	table, so every consumer of it is a POLLER, and this system already learned
//	what polling Postgres costs: the monitoring export showed ~51,000 reads
//	against 319 actual marks, and an always-awake Neon compute behind them. A
//	stream inverts that — the machine that took the write tells everyone else,
//	once, instead of everyone else asking the database forever.
//
// POSTGRES REMAINS THE SYSTEM OF RECORD. NOTHING HERE IS AUTHORITATIVE.
//
//	The audit row is co-committed inside the business transaction (store.WithAudit)
//	and that is unchanged and unchangeable. What this package adds is a MIRROR,
//	published AFTER that transaction commits. If the mirror is lost, the ledger is
//	still complete and the system is still correct; a consumer simply missed a
//	notification. That asymmetry is the whole design, and it is why Publish
//	cannot return an error — see the Publisher interface.
package events

import (
	"context"
	"encoding/json"
	"time"
)

// SchemaVersion is the envelope shape's version, carried on every message.
//
// It is on the ENVELOPE rather than only in the topic name because the topic
// name is chosen by the producer and read by a consumer that may be older: a
// consumer that meets a version it does not understand can skip the record and
// say so, instead of mis-parsing it. The topic suffix (od.audit.v1) versions the
// STREAM; this versions the record.
const SchemaVersion = 1

// Envelope is one domain event on the wire.
//
// The field set is deliberately the audit row's field set and nothing more. A
// richer, per-event-type Go struct was the obvious alternative and is the wrong
// trade here: it would mean a new type (and a new marshaller, and a new test)
// for each of the two dozen registered actions, all to restate what the action's
// own Payload already carries. Consumers that need the specifics parse Payload,
// which is the SAME bytes the ledger stored — so a consumer and an auditor
// reading the same event can never see two different accounts of it.
type Envelope struct {
	// ID is unique per PUBLISH, not per fact. Two publishes of the same audit
	// row (a retry, a replay) carry different IDs and the same Type/TargetID, so
	// a consumer deduplicates on what it actually cares about rather than on a
	// number the producer happened to mint twice.
	ID string `json:"id"`
	// Type is the domain.Action wire name — "od.created", "auth.login_succeeded".
	// It is the same closed registry the audit ledger validates against, so a
	// topic can never carry an event type nobody declared.
	Type string `json:"type"`
	// SchemaVersion is SchemaVersion at the time of publish.
	SchemaVersion int `json:"schema_version"`
	// OccurredAt is when the producing process observed the commit, in UTC.
	//
	// NOT the audit row's created_at, which is the database clock and is the
	// authoritative one. This is deliberately the weaker of the two: the
	// producer does not read the row back after committing it (that would be a
	// query per write, which is the cost this package exists to avoid), so it
	// reports what it knows and lets a consumer that needs the exact ledger time
	// go and read the ledger.
	OccurredAt time.Time `json:"occurred_at"`
	// Source identifies the producing process — the Fly machine id where there is
	// one. It is what lets a consumer ignore its OWN messages, which matters for
	// the cache bus: a broadcast invalidation must not bounce back and clear the
	// cache of the machine that already cleared it.
	Source string `json:"source"`

	ActorAccount string          `json:"actor_account,omitempty"`
	ActorRole    string          `json:"actor_role,omitempty"`
	TargetTable  string          `json:"target_table,omitempty"`
	TargetID     string          `json:"target_id,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}

// Key is the partition key: Kafka orders records within a partition and nowhere
// else, so this decides what "in order" means for a consumer.
//
// TargetID first, because the ordering that can actually be got wrong is
// per-ROW: a create and a later removal of the same od_records row must not be
// consumed backwards. Falling back to the actor keeps one coordinator's stream
// coherent when there is no target (the auth events have none). Falling back to
// the type is the last resort — it keeps a whole event type on one partition,
// which is slow but never wrong.
func (e Envelope) Key() string {
	switch {
	case e.TargetID != "":
		return e.TargetID
	case e.ActorAccount != "":
		return e.ActorAccount
	default:
		return e.Type
	}
}

// Publisher hands an event to whatever carries it off this process.
//
// PUBLISH RETURNS NOTHING, AND THAT IS THE LOAD-BEARING PART OF THIS INTERFACE.
//
//	Every caller is on a request path that has ALREADY COMMITTED. The write
//	succeeded, the audit row is durable, the student is marked. There is no
//	honest thing a caller could do with a delivery error at that point: it cannot
//	un-commit, and returning 500 for a successful mark because a broker was
//	briefly unreachable would turn an optional mirror into a new way for the
//	marking path to fail on the one day it must not.
//
//	So the interface refuses to offer the error. A dropped event is counted and
//	logged where it happens (see AsyncPublisher), which is where the operator can
//	actually see it, rather than propagated to a call site that must ignore it.
//
// Publish must also NOT BLOCK. Implementations buffer.
type Publisher interface {
	Publish(ctx context.Context, envelope Envelope)
}

// Nop is the publisher used when the stream is switched off.
//
// This is the DEFAULT, not a test double. With KAFKA_BROKERS unset — which is
// how production runs until someone deliberately turns the stream on — every
// publish site in the codebase calls this, allocating nothing and doing nothing.
// The alternative (a nil Publisher and a nil check at each call site) puts a
// panic one forgotten check away from the marking path.
type Nop struct{}

// Publish discards the envelope.
func (Nop) Publish(context.Context, Envelope) {}

var _ Publisher = Nop{}
