//go:build stream

// Package streamtest is the broker-backed acceptance suite for the domain event
// stream.
//
// It has its OWN build tag (`stream`) rather than reusing `integration`, because
// it needs a different dependency: `integration` means "a Postgres is running",
// and these need a Kafka. Folding them together would make the whole integration
// suite fail on a machine that has a database but no broker, which is the normal
// state of every machine in this project.
//
// Run it with:  make kafka-up && make test-stream
//
// Priority tags: [P0] critical gate · [P1] important · [P2] guard.
package streamtest

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Shruhath/OD-Amrita/api/internal/cachebus"
	"github.com/Shruhath/OD-Amrita/api/internal/events"
	"github.com/Shruhath/OD-Amrita/api/internal/kafka"
)

// configOrSkip yields a live broker config, or skips — so a machine without a
// broker stays clean rather than red.
func configOrSkip(t *testing.T) kafka.Config {
	t.Helper()
	if os.Getenv("KAFKA_BROKERS") == "" {
		t.Skip("KAFKA_BROKERS not set; skipping the broker round trip (run `make kafka-up`)")
	}
	cfg, err := kafka.LoadConfig()
	if err != nil {
		t.Fatalf("kafka config: %v", err)
	}
	return cfg
}

// collector accumulates what a consumer saw, safely across the consumer's own
// goroutine and the test's.
type collector struct {
	mu   sync.Mutex
	seen []events.Envelope
}

func (c *collector) handle(_ context.Context, envelope events.Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, envelope)
	return nil
}

func (c *collector) find(id string) (events.Envelope, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, envelope := range c.seen {
		if envelope.ID == id {
			return envelope, true
		}
	}
	return events.Envelope{}, false
}

// startConsumer runs a consumer in the background and returns its collector.
func startConsumer(t *testing.T, cfg kafka.Config, opts kafka.ConsumerOptions) *collector {
	t.Helper()
	consumer, err := kafka.NewConsumer(cfg, opts)
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	got := &collector{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = consumer.Run(ctx, got.handle)
	}()
	t.Cleanup(func() {
		cancel()
		consumer.Close()
		<-done
	})
	// A groupless consumer starts at the END of the log, so it must be polling
	// BEFORE anything is produced or it will never see it. There is no "I am
	// subscribed" signal to wait on, so this waits out the initial metadata
	// fetch and join. It is the one unavoidable sleep in this suite.
	time.Sleep(2 * time.Second)
	return got
}

// waitFor polls until the predicate holds or the deadline passes.
func waitFor(t *testing.T, what string, deadline time.Duration, ok func() bool) {
	t.Helper()
	limit := time.Now().Add(deadline)
	for time.Now().Before(limit) {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", deadline, what)
}

// TestRoundTrip_AMarkReachesBothTopicsIntact — [P0].
//
// The end-to-end claim of the whole change: an event published by the api is
// readable by another process, on both the topics it routes to, with its payload
// unchanged. Everything else in this package is unit-tested against fakes; this
// is the one test that would catch a real serialization, key or routing mistake.
func TestRoundTrip_AMarkReachesBothTopicsIntact(t *testing.T) {
	cfg := configOrSkip(t)

	auditSeen := startConsumer(t, cfg, kafka.ConsumerOptions{Topics: []string{events.TopicAudit}})
	marksSeen := startConsumer(t, cfg, kafka.ConsumerOptions{Topics: []string{events.TopicMarks}})

	sink, err := kafka.NewSink(cfg, "test-machine")
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	publisher := events.NewAsyncPublisher(sink, events.AsyncPublisherOptions{
		BatchSize: 1, LingerFor: 10 * time.Millisecond,
	})

	sent := events.Envelope{
		ID:           "round-trip-" + time.Now().UTC().Format("150405.000000"),
		Type:         "od.created",
		ActorAccount: "coord@amrita.edu",
		ActorRole:    "coordinator",
		TargetTable:  "od_records",
		TargetID:     "record-round-trip",
		Payload:      json.RawMessage(`{"slot":3,"capture_method":"nfc"}`),
	}
	publisher.Publish(context.Background(), sent)

	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	publisher.Close(closeCtx)

	waitFor(t, "the event on the audit mirror", 15*time.Second, func() bool {
		_, ok := auditSeen.find(sent.ID)
		return ok
	})
	waitFor(t, "the event on the marks stream", 15*time.Second, func() bool {
		_, ok := marksSeen.find(sent.ID)
		return ok
	})

	got, _ := auditSeen.find(sent.ID)
	if got.Type != sent.Type || got.TargetID != sent.TargetID || got.ActorAccount != sent.ActorAccount {
		t.Errorf("[P0] envelope came back changed: %+v", got)
	}
	if string(got.Payload) != string(sent.Payload) {
		t.Errorf("[P0] payload = %s, want %s — a consumer and an auditor must read the same bytes", got.Payload, sent.Payload)
	}
	if got.Source != "test-machine" {
		t.Errorf("[P0] Source = %q, want the producing machine — the cache bus depends on it", got.Source)
	}
	if got.SchemaVersion != events.SchemaVersion {
		t.Errorf("[P0] SchemaVersion = %d, want %d", got.SchemaVersion, events.SchemaVersion)
	}
	if publisher.Dropped() != 0 || publisher.Failed() != 0 {
		t.Errorf("[P0] dropped=%d failed=%d, want 0", publisher.Dropped(), publisher.Failed())
	}
}

// TestRoundTrip_NonAttendanceEventsStayOffTheMarksStream — [P1]. The routing
// rule, proven against a real broker rather than against TopicsFor alone.
func TestRoundTrip_NonAttendanceEventsStayOffTheMarksStream(t *testing.T) {
	cfg := configOrSkip(t)

	auditSeen := startConsumer(t, cfg, kafka.ConsumerOptions{Topics: []string{events.TopicAudit}})
	marksSeen := startConsumer(t, cfg, kafka.ConsumerOptions{Topics: []string{events.TopicMarks}})

	sink, err := kafka.NewSink(cfg, "test-machine")
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	publisher := events.NewAsyncPublisher(sink, events.AsyncPublisherOptions{
		BatchSize: 1, LingerFor: 10 * time.Millisecond,
	})

	login := events.Envelope{
		ID:           "login-" + time.Now().UTC().Format("150405.000000"),
		Type:         "auth.login_succeeded",
		ActorAccount: "coord@amrita.edu",
	}
	publisher.Publish(context.Background(), login)
	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	publisher.Close(closeCtx)

	waitFor(t, "the login on the audit mirror", 15*time.Second, func() bool {
		_, ok := auditSeen.find(login.ID)
		return ok
	})
	// It is already on the audit topic, and both consumers were polling
	// throughout, so if it were going to appear on the marks stream it would have
	// by now.
	if _, ok := marksSeen.find(login.ID); ok {
		t.Error("[P1] an auth event reached the marks stream — its consumers would have to filter a firehose")
	}
}

// TestRoundTrip_TheCacheBusReachesAPeerMachine — [P0].
//
// THE reason the bus exists. Two Bus values with different sources stand in for
// the two Fly machines. Before this, machine B kept serving the pre-change
// answer for the rest of the cache window, and which answer a coordinator got
// depended on which machine their poll landed on.
func TestRoundTrip_TheCacheBusReachesAPeerMachine(t *testing.T) {
	cfg := configOrSkip(t)

	sink, err := kafka.NewSink(cfg, "machine-a")
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	publisher := events.NewAsyncPublisher(sink, events.AsyncPublisherOptions{
		BatchSize: 1, LingerFor: 10 * time.Millisecond,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		publisher.Close(ctx)
	})

	// Machine B: consumes the cache topic and applies what it hears.
	busB := cachebus.New(nil, "machine-b", func(string, ...any) {})
	peerCache := &countingCache{}
	busB.Register(cachebus.ScopeMarkingScope, peerCache)

	consumerB, err := kafka.NewConsumer(cfg, kafka.ConsumerOptions{Topics: []string{events.TopicCache}})
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = consumerB.Run(ctx, busB.Handle)
	}()
	t.Cleanup(func() {
		cancel()
		consumerB.Close()
		<-done
	})
	time.Sleep(2 * time.Second)

	// Machine A: an administrator archives an event.
	busA := cachebus.New(publisher, "machine-a", func(string, ...any) {})
	localCache := &countingCache{}
	busA.Register(cachebus.ScopeMarkingScope, localCache)
	busA.Invalidate(context.Background(), "event archived", cachebus.ScopeMarkingScope)

	if localCache.cleared() != 1 {
		t.Fatalf("[P0] the machine taking the write cleared %d times, want 1", localCache.cleared())
	}
	waitFor(t, "the peer machine to drop its cache", 15*time.Second, func() bool {
		return peerCache.cleared() >= 1
	})
	if peerCache.cleared() != 1 {
		t.Errorf("[P0] the peer cleared %d times, want exactly 1", peerCache.cleared())
	}
}

// TestRoundTrip_AMachineIgnoresItsOwnBroadcast — [P1]. Over a real broker, since
// this is where the echo actually comes from.
func TestRoundTrip_AMachineIgnoresItsOwnBroadcast(t *testing.T) {
	cfg := configOrSkip(t)

	sink, err := kafka.NewSink(cfg, "machine-solo")
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	publisher := events.NewAsyncPublisher(sink, events.AsyncPublisherOptions{
		BatchSize: 1, LingerFor: 10 * time.Millisecond,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		publisher.Close(ctx)
	})

	bus := cachebus.New(publisher, "machine-solo", func(string, ...any) {})
	cache := &countingCache{}
	bus.Register(cachebus.ScopeRevocation, cache)

	consumer, err := kafka.NewConsumer(cfg, kafka.ConsumerOptions{Topics: []string{events.TopicCache}})
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = consumer.Run(ctx, bus.Handle)
	}()
	t.Cleanup(func() {
		cancel()
		consumer.Close()
		<-done
	})
	time.Sleep(2 * time.Second)

	bus.Invalidate(context.Background(), "session revoked", cachebus.ScopeRevocation)

	// A message from ANOTHER machine, published second. Waiting for THIS one to
	// land is what proves the pipe is actually working — without it this test
	// passes just as happily when nothing is delivered at all, which is exactly
	// how the cache-topic routing bug survived a green run of this suite.
	peer := events.Envelope{
		ID:            "peer-" + time.Now().UTC().Format("150405.000000"),
		Type:          events.TypeCacheInvalidated,
		SchemaVersion: events.SchemaVersion,
		Source:        "machine-other",
		Payload:       json.RawMessage(`{"scopes":["revocation"],"reason":"peer revoke"}`),
	}
	publisher.Publish(context.Background(), peer)

	waitFor(t, "the peer's invalidation to arrive", 15*time.Second, func() bool {
		return cache.cleared() >= 2
	})
	if cache.cleared() != 2 {
		t.Errorf("[P0] cleared %d times, want exactly 2 (one local, one from the peer) — a third would mean the machine re-applied its own broadcast, discarding entries it had just repopulated", cache.cleared())
	}
}

type countingCache struct {
	mu    sync.Mutex
	count int
}

func (c *countingCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
}

func (c *countingCache) cleared() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}
