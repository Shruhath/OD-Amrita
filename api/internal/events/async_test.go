package events

// async_test.go — UNIT tests. No build tag, no broker, no network: the whole
// point of putting the buffering above the transport is that every rule it
// encodes can be proven with a fake sink.
//
// Invoked by: api-ci -> build job.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// blockingSink lets a test hold Send open, which is how "the sink cannot keep
// up" is simulated deterministically rather than by hoping a real one is slow.
type blockingSink struct {
	mu       sync.Mutex
	batches  [][]Envelope
	release  chan struct{}
	sendErr  error
	sendCall chan struct{}
}

func newBlockingSink() *blockingSink {
	return &blockingSink{
		release:  make(chan struct{}),
		sendCall: make(chan struct{}, 128),
	}
}

func (s *blockingSink) Send(_ context.Context, envelopes []Envelope) error {
	s.sendCall <- struct{}{}
	<-s.release
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, envelopes)
	return s.sendErr
}

func (s *blockingSink) Close() {}

func (s *blockingSink) sent() [][]Envelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]Envelope, len(s.batches))
	copy(out, s.batches)
	return out
}

// recordingSink accepts everything immediately.
type recordingSink struct {
	mu      sync.Mutex
	batches [][]Envelope
	err     error
	closed  bool
}

func (s *recordingSink) Send(_ context.Context, envelopes []Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, envelopes)
	return s.err
}

func (s *recordingSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, batch := range s.batches {
		n += len(batch)
	}
	return n
}

func discardLog(string, ...any) {}

// TestAsyncPublisher_PublishNeverBlocks is THE test for this file. [P0]
//
// Publish is called from the marking path after a mark has already committed. If
// it could block, a broker that stopped responding would stop a coordinator's
// phone from responding, and it would do so at exactly the moment the system is
// under load. The sink here never returns until the test lets it, and the
// publisher is given a buffer of one — so every call after the first two has
// nowhere to go and MUST return anyway.
func TestAsyncPublisher_PublishNeverBlocksWhenTheSinkIsStuck(t *testing.T) {
	sink := newBlockingSink()
	publisher := NewAsyncPublisher(sink, AsyncPublisherOptions{
		BufferSize: 1,
		BatchSize:  1,
		LingerFor:  time.Millisecond,
		Logf:       discardLog,
	})
	defer func() {
		close(sink.release)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		publisher.Close(ctx)
	}()

	// Wait until the sender is actually parked inside Send, so the buffer is the
	// only place left for anything to go.
	publisher.Publish(context.Background(), Envelope{Type: "od.created"})
	select {
	case <-sink.sendCall:
	case <-time.After(2 * time.Second):
		t.Fatal("[P0] sender never reached the sink")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			publisher.Publish(context.Background(), Envelope{Type: "od.created"})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("[P0] Publish BLOCKED while the sink was stuck — a stalled broker would stall the marking path")
	}
	if publisher.Dropped() == 0 {
		t.Error("[P0] expected drops once the buffer filled; a publisher that neither blocked nor dropped is not accounting for the events")
	}
}

// TestAsyncPublisher_DroppedEventsAreCounted — [P1] the drop must be visible.
// Silence would make a broker outage indistinguishable from an idle system.
func TestAsyncPublisher_DroppedEventsAreCounted(t *testing.T) {
	sink := newBlockingSink()
	publisher := NewAsyncPublisher(sink, AsyncPublisherOptions{
		BufferSize: 1, BatchSize: 1, LingerFor: time.Millisecond, Logf: discardLog,
	})
	defer func() {
		close(sink.release)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		publisher.Close(ctx)
	}()

	publisher.Publish(context.Background(), Envelope{Type: "od.created"})
	<-sink.sendCall
	for i := 0; i < 50; i++ {
		publisher.Publish(context.Background(), Envelope{Type: "od.created"})
	}
	if got := publisher.Dropped(); got < 40 {
		t.Errorf("[P1] Dropped() = %d, want the great majority of 50 rejected while the buffer held 1", got)
	}
}

// TestAsyncPublisher_BatchesAndFlushesOnClose — [P1]. Close must flush what is
// buffered, or a deploy would silently lose the last few seconds of events.
func TestAsyncPublisher_BatchesAndFlushesOnClose(t *testing.T) {
	sink := &recordingSink{}
	publisher := NewAsyncPublisher(sink, AsyncPublisherOptions{
		BufferSize: 64,
		BatchSize:  1000, // never reached, so only the linger or Close can flush
		LingerFor:  time.Hour,
		Logf:       discardLog,
	})
	for i := 0; i < 10; i++ {
		publisher.Publish(context.Background(), Envelope{Type: "od.created"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	publisher.Close(ctx)

	if got := sink.count(); got != 10 {
		t.Errorf("[P1] sink received %d events, want 10 flushed by Close", got)
	}
	if !sink.closed {
		t.Error("[P1] Close must close the sink, or the transport's own flush never runs")
	}
}

// TestAsyncPublisher_LingerFlushesAPartialBatch — [P2]. Without this, a single
// mark on a quiet day would sit in the buffer until another one arrived.
func TestAsyncPublisher_LingerFlushesAPartialBatch(t *testing.T) {
	sink := &recordingSink{}
	publisher := NewAsyncPublisher(sink, AsyncPublisherOptions{
		BufferSize: 64, BatchSize: 1000, LingerFor: 20 * time.Millisecond, Logf: discardLog,
	})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		publisher.Close(ctx)
	}()

	publisher.Publish(context.Background(), Envelope{Type: "od.created"})

	deadline := time.After(2 * time.Second)
	for sink.count() == 0 {
		select {
		case <-deadline:
			t.Fatal("[P2] a lone event never flushed; it would wait for company that may not come")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// TestAsyncPublisher_SendFailureIsCountedAndDoesNotStopTheSender — [P1].
// A broker that rejects one batch must not take the stream down with it.
func TestAsyncPublisher_SendFailureIsCountedAndDoesNotStopTheSender(t *testing.T) {
	sink := &recordingSink{err: errors.New("broker said no")}
	publisher := NewAsyncPublisher(sink, AsyncPublisherOptions{
		BufferSize: 64, BatchSize: 1, LingerFor: time.Millisecond, Logf: discardLog,
	})
	for i := 0; i < 5; i++ {
		publisher.Publish(context.Background(), Envelope{Type: "od.created"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	publisher.Close(ctx)

	if publisher.Failed() == 0 {
		t.Error("[P1] a rejected batch must be counted in Failed()")
	}
	if publisher.Sent() != 0 {
		t.Errorf("[P1] Sent() = %d, want 0 — a rejected batch was not sent", publisher.Sent())
	}
	if sink.count() < 5 {
		t.Errorf("[P1] sender stopped after a failure: sink saw %d of 5", sink.count())
	}
}

// TestAsyncPublisher_StampsSchemaVersionAndTime — [P2]. A consumer branches on
// SchemaVersion, so an unstamped zero would read as "older than v1" and be
// treated as parseable by every future build.
func TestAsyncPublisher_StampsSchemaVersionAndTime(t *testing.T) {
	sink := &recordingSink{}
	publisher := NewAsyncPublisher(sink, AsyncPublisherOptions{
		BufferSize: 8, BatchSize: 1, LingerFor: time.Millisecond, Logf: discardLog,
	})
	publisher.Publish(context.Background(), Envelope{Type: "od.created"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	publisher.Close(ctx)

	batches := sink.batches
	if len(batches) == 0 || len(batches[0]) == 0 {
		t.Fatal("[P2] nothing reached the sink")
	}
	got := batches[0][0]
	if got.SchemaVersion != SchemaVersion {
		t.Errorf("[P2] SchemaVersion = %d, want %d", got.SchemaVersion, SchemaVersion)
	}
	if got.OccurredAt.IsZero() {
		t.Error("[P2] OccurredAt must be stamped")
	}
}

// TestAsyncPublisher_BatchesAreNotAliased — [P0] a correctness trap, not a
// style point. run() reuses one slice for every batch. A sink that keeps the
// slice it was handed (franz-go's async produce does exactly that) would see its
// records overwritten by the NEXT batch — publishing one student's mark under
// another student's envelope. send() copies; this proves it still does.
func TestAsyncPublisher_BatchesAreNotAliased(t *testing.T) {
	sink := &recordingSink{}
	publisher := NewAsyncPublisher(sink, AsyncPublisherOptions{
		BufferSize: 64, BatchSize: 2, LingerFor: time.Millisecond, Logf: discardLog,
	})
	for i := 0; i < 8; i++ {
		publisher.Publish(context.Background(), Envelope{Type: "od.created", TargetID: string(rune('a' + i))})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	publisher.Close(ctx)

	seen := map[string]int{}
	for _, batch := range sink.batches {
		for _, envelope := range batch {
			seen[envelope.TargetID]++
		}
	}
	for i := 0; i < 8; i++ {
		id := string(rune('a' + i))
		if seen[id] != 1 {
			t.Errorf("[P0] target %q appeared %d times, want exactly 1 — batches are aliased", id, seen[id])
		}
	}
}

// TestNop_IsTheDefaultAndDoesNothing — [P2]. This is the production path until
// somebody sets KAFKA_BROKERS, so it must be inert rather than merely quiet.
func TestNop_IsTheDefaultAndDoesNothing(t *testing.T) {
	var publisher Publisher = Nop{}
	publisher.Publish(context.Background(), Envelope{Type: "od.created"})
}
