package events

// topics.go — which stream an event goes to.

import "strings"

// The topics this system produces. The `.v1` suffix versions the STREAM: a
// breaking change to what a topic carries gets a new topic, so an old consumer
// keeps reading a stream that still means what it did when it was written,
// instead of quietly misreading a new one.
const (
	// TopicAudit carries EVERY committed domain event, unfiltered. It is the
	// mirror of the audit ledger and the topic a general consumer subscribes to.
	TopicAudit = "od.audit.v1"
	// TopicMarks carries only attendance-record events. It exists as a separate
	// stream, not a filter over TopicAudit, because its consumers are the ones
	// that must never fall behind — a live board, an export — and putting them on
	// their own topic means a slow audit-archival consumer cannot delay them.
	TopicMarks = "od.marks.v1"
	// TopicCache carries cross-machine cache invalidations. See package cachebus.
	// It is separate for the opposite reason: its messages are worthless after a
	// few seconds, so it wants a short retention that would be wrong for the
	// other two.
	TopicCache = "od.cache.v1"
)

// AllTopics is every topic this system produces, for the provisioning check and
// for a consumer that wants the lot.
var AllTopics = []string{TopicAudit, TopicMarks, TopicCache}

// TypeCacheInvalidated is the cache bus's event type.
//
// It lives HERE rather than in package cachebus, and that placement is the fix
// for a real bug this file had: routing is decided by event type, so a type the
// router does not know about falls through to the audit mirror. Cache
// invalidations were therefore being produced to od.audit.v1 — a topic the api's
// bus consumer does not subscribe to — so the broadcast silently reached nobody
// while polluting the audit stream with instructions that are not domain facts.
// The broker round trip caught it; nothing below it could, because every unit
// test on both sides was passing.
//
// Keeping the constant next to the routing table is what makes the two
// impossible to change independently.
const TypeCacheInvalidated = "cache.invalidated"

// markPrefixes are the action families that also reach TopicMarks.
//
// Matched on the "od." prefix rather than an enumerated list of the three
// current od.* actions on purpose: this is a routing hint, and a new od.*
// action added by a later change should reach the marks stream automatically. A
// missed routing entry would be silent — the event would still be on the audit
// topic and nobody would notice until a board went stale.
var markPrefixes = []string{"od."}

// TopicsFor returns every topic an event of this type is published to.
//
// An event can land on MORE THAN ONE topic, and that is intended: od.created
// goes to both the audit mirror and the marks stream. Kafka has no server-side
// fanout to give here, so the producer writes it twice — two small records, on a
// path that already batches, in exchange for consumers that never have to filter
// a firehose to find the events they care about.
func TopicsFor(eventType string) []string {
	// The cache bus is NOT a domain event and must never reach the audit mirror:
	// it is an instruction with an hour of retention, not a fact about a student.
	// This branch returns early rather than adding to the default list, so the
	// exclusion is a property of the code rather than of remembering to filter.
	if eventType == TypeCacheInvalidated {
		return []string{TopicCache}
	}

	topics := []string{TopicAudit}
	for _, prefix := range markPrefixes {
		if strings.HasPrefix(eventType, prefix) {
			topics = append(topics, TopicMarks)
			break
		}
	}
	return topics
}
