package events

// topics_test.go — UNIT tests for routing and partition keys.
//
// Invoked by: api-ci -> build job.

import (
	"slices"
	"testing"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

// TestTopicsFor_EveryRegisteredActionReachesTheAuditTopic — [P0].
//
// The audit topic is meant to be the COMPLETE mirror of the ledger. A routing
// rule that quietly excluded an action would leave a consumer believing it had
// seen everything, which is the worst possible failure for an audit stream: not
// an error, just a gap. Driving this from domain.AllActions means an action
// added later is covered without anybody remembering to extend this test.
func TestTopicsFor_EveryRegisteredActionReachesTheAuditTopic(t *testing.T) {
	for _, action := range domain.AllActions {
		topics := TopicsFor(string(action))
		if !slices.Contains(topics, TopicAudit) {
			t.Errorf("[P0] action %q routes to %v, which does not include the audit mirror", action, topics)
		}
	}
}

// TestTopicsFor_AttendanceActionsAlsoReachTheMarksTopic — [P1].
func TestTopicsFor_AttendanceActionsAlsoReachTheMarksTopic(t *testing.T) {
	marks := []domain.Action{domain.ActionODCreated, domain.ActionODOverridden, domain.ActionODRemoved}
	for _, action := range marks {
		topics := TopicsFor(string(action))
		if !slices.Contains(topics, TopicMarks) {
			t.Errorf("[P1] %q routes to %v, want the marks stream too", action, topics)
		}
	}
}

// TestTopicsFor_NonAttendanceActionsStayOffTheMarksTopic — [P1]. The marks
// stream exists so its consumers do not have to filter a firehose; letting
// unrelated events onto it would defeat the reason it is a separate topic.
func TestTopicsFor_NonAttendanceActionsStayOffTheMarksTopic(t *testing.T) {
	others := []domain.Action{
		domain.ActionAuthLoginSucceeded,
		domain.ActionEventArchived,
		domain.ActionUserProvisioned,
		domain.ActionScanBlocked,
		domain.ActionDisputeSubmitted,
	}
	for _, action := range others {
		topics := TopicsFor(string(action))
		if slices.Contains(topics, TopicMarks) {
			t.Errorf("[P1] %q must not reach the marks stream, got %v", action, topics)
		}
	}
}

// TestTopicsFor_CacheTopicIsNeverAProduceTargetForDomainEvents — [P0].
//
// The cache topic has a ONE HOUR retention and is consumed by every api machine
// with no group. A domain event routed there would be broadcast to every machine
// as if it were an invalidation instruction and then deleted within the hour.
func TestTopicsFor_CacheTopicIsNeverAProduceTargetForDomainEvents(t *testing.T) {
	for _, action := range domain.AllActions {
		if slices.Contains(TopicsFor(string(action)), TopicCache) {
			t.Errorf("[P0] action %q must never route to the cache-invalidation topic", action)
		}
	}
}

// TestEnvelopeKey_PrefersTheTargetRow — [P1].
//
// Kafka orders within a partition and nowhere else, so this decides whether a
// create and a later removal of the SAME record can be consumed out of order.
func TestEnvelopeKey_PrefersTheTargetRow(t *testing.T) {
	cases := []struct {
		name     string
		envelope Envelope
		want     string
	}{
		{
			name:     "target id wins, so one record's events stay ordered",
			envelope: Envelope{Type: "od.created", TargetID: "record-1", ActorAccount: "coord@amrita.edu"},
			want:     "record-1",
		},
		{
			name:     "actor when there is no target (the auth events have none)",
			envelope: Envelope{Type: "auth.login_succeeded", ActorAccount: "coord@amrita.edu"},
			want:     "coord@amrita.edu",
		},
		{
			name:     "type as the last resort — slow, never wrong",
			envelope: Envelope{Type: "sync.completed"},
			want:     "sync.completed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.envelope.Key(); got != tc.want {
				t.Errorf("[P1] Key() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAllTopicsIsComplete — [P2]. EnsureTopics provisions from a separate list;
// a topic produced to but never provisioned would rely on auto-creation and get
// the broker's default retention, which for the cache topic is badly wrong.
func TestAllTopicsIsComplete(t *testing.T) {
	for _, topic := range []string{TopicAudit, TopicMarks, TopicCache} {
		if !slices.Contains(AllTopics, topic) {
			t.Errorf("[P2] AllTopics is missing %q", topic)
		}
	}
	if len(AllTopics) != 3 {
		t.Errorf("[P2] AllTopics has %d entries; add the new topic to the provisioning specs too", len(AllTopics))
	}
}

// TestTopicsFor_CacheInvalidationsGoOnlyToTheCacheTopic — [P0] REGRESSION.
//
// This is the bug the broker round trip found and nothing below it could. The
// router's default is the audit mirror, so an event type it does not recognise
// falls through to it — and cache.invalidated, declared in another package, was
// exactly such a type. Every unit test on both sides passed while the api's bus
// consumer (subscribed to the cache topic) received nothing at all, and the
// audit mirror filled with instructions that are not domain facts.
//
// Two assertions, because each half is separately wrong: reaching the cache
// topic is what makes the bus work, and staying OFF the audit mirror is what
// keeps the audit stream an audit stream.
func TestTopicsFor_CacheInvalidationsGoOnlyToTheCacheTopic(t *testing.T) {
	topics := TopicsFor(TypeCacheInvalidated)

	if !slices.Contains(topics, TopicCache) {
		t.Errorf("[P0] cache invalidations route to %v, which does not include the cache topic — the broadcast would reach no consumer", topics)
	}
	if slices.Contains(topics, TopicAudit) {
		t.Errorf("[P0] cache invalidations route to %v, which includes the AUDIT mirror — an instruction is not a domain fact", topics)
	}
	if slices.Contains(topics, TopicMarks) {
		t.Errorf("[P0] cache invalidations must not reach the marks stream, got %v", topics)
	}
}

// TestTypeCacheInvalidated_IsNotADomainAction — [P1]. The audit ledger validates
// against a closed registry; an invalidation must never be able to enter it.
func TestTypeCacheInvalidated_IsNotADomainAction(t *testing.T) {
	if domain.Action(TypeCacheInvalidated).Registered() {
		t.Error("[P1] cache.invalidated is registered as an auditable action — it is an instruction, not a fact, and would be written into the immutable ledger")
	}
}
