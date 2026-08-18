package events

// async.go — the buffering publisher. It is what makes Publish safe to call from
// a request path: the caller hands over an envelope and returns immediately,
// and a background goroutine does the network work.

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Sink is the transport under an AsyncPublisher — the seam the Kafka client
// plugs into.
//
// It takes a BATCH because that is the unit the network work is actually worth
// doing in, and it returns an error because at this layer, unlike Publisher,
// there is something sensible to do with one: count it, log it, and move on.
// A test substitutes a fake sink and exercises every buffering and drop path in
// this file without a broker.
type Sink interface {
	Send(ctx context.Context, envelopes []Envelope) error
	Close()
}

// AsyncPublisherOptions tunes the buffer. The zero value is usable; defaults
// below apply to any field left at zero.
type AsyncPublisherOptions struct {
	// BufferSize bounds the queue between the request paths and the sender.
	BufferSize int
	// BatchSize is the most envelopes handed to the sink in one Send.
	BatchSize int
	// LingerFor is how long a partial batch waits for company before being sent
	// anyway. It bounds the delay this adds to an event's arrival.
	LingerFor time.Duration
	// SendTimeout bounds one Send, so a hung broker cannot wedge the sender
	// goroutine forever and silently stop the stream.
	SendTimeout time.Duration
	// Logf receives drop and failure reports. Defaults to the standard logger.
	Logf func(format string, args ...any)
}

// The defaults.
//
// BufferSize 4096: an order of magnitude more than the busiest measured minute
// on this system (319 marks across a whole event day), so the buffer is a shock
// absorber for a broker blip rather than a queue that is expected to run deep.
//
// LingerFor 200ms: below the threshold where a person watching a live board
// would notice, and long enough that a burst of marks arriving together travels
// as one produce request rather than a dozen.
const (
	defaultBufferSize  = 4096
	defaultBatchSize   = 256
	defaultLingerFor   = 200 * time.Millisecond
	defaultSendTimeout = 10 * time.Second
)

// AsyncPublisher buffers envelopes and sends them in batches on one goroutine.
//
// WHAT HAPPENS WHEN THE BUFFER IS FULL: the envelope is DROPPED and counted.
//
//	This is the decision that matters in the whole package, and blocking is the
//	tempting alternative. Blocking would mean that a slow or dead broker applies
//	backpressure through the buffer, into Publish, into the request handler, and
//	out to a coordinator standing in front of a student with a phone that will
//	not respond. The event stream is a mirror of a ledger that is already durable
//	in Postgres; it is never worth one second of the marking path. So it drops,
//	loudly, and Dropped() is the number an operator reads to find out.
type AsyncPublisher struct {
	queue   chan Envelope
	sink    Sink
	opts    AsyncPublisherOptions
	logf    func(string, ...any)
	dropped atomic.Uint64
	failed  atomic.Uint64
	sent    atomic.Uint64

	closeOnce sync.Once
	done      chan struct{}
}

// NewAsyncPublisher starts the sender goroutine. Close stops it.
func NewAsyncPublisher(sink Sink, opts AsyncPublisherOptions) *AsyncPublisher {
	if opts.BufferSize <= 0 {
		opts.BufferSize = defaultBufferSize
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultBatchSize
	}
	if opts.LingerFor <= 0 {
		opts.LingerFor = defaultLingerFor
	}
	if opts.SendTimeout <= 0 {
		opts.SendTimeout = defaultSendTimeout
	}
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}
	p := &AsyncPublisher{
		queue: make(chan Envelope, opts.BufferSize),
		sink:  sink,
		opts:  opts,
		logf:  logf,
		done:  make(chan struct{}),
	}
	go p.run()
	return p
}

// Publish queues the envelope, or drops it if the queue is full.
//
// The select's default case is what makes this non-blocking; there is
// deliberately no timeout branch, because "wait a little" on the marking path is
// the same mistake as "wait" with a smaller number on it.
func (p *AsyncPublisher) Publish(_ context.Context, envelope Envelope) {
	if envelope.SchemaVersion == 0 {
		envelope.SchemaVersion = SchemaVersion
	}
	if envelope.OccurredAt.IsZero() {
		envelope.OccurredAt = time.Now().UTC()
	}
	select {
	case p.queue <- envelope:
	default:
		// Report the FIRST drop and then every 100th. A broker outage produces a
		// drop per event, and a log line per event would bury the outage in its
		// own symptoms — while silence would hide it entirely.
		if n := p.dropped.Add(1); n == 1 || n%100 == 0 {
			p.logf("events: buffer full, dropped %d event(s) — the audit ledger is unaffected; latest %q", n, envelope.Type)
		}
	}
}

// run drains the queue into batches until the queue is closed.
func (p *AsyncPublisher) run() {
	defer close(p.done)

	batch := make([]Envelope, 0, p.opts.BatchSize)
	timer := time.NewTimer(p.opts.LingerFor)
	defer timer.Stop()
	// A timer is only armed while a partial batch is waiting. An always-running
	// ticker would wake this goroutine every LingerFor forever, which on an idle
	// night is exactly the kind of pointless wakeup the rest of this system spent
	// a release removing.
	timerArmed := false

	flush := func() {
		if len(batch) == 0 {
			return
		}
		p.send(batch)
		batch = batch[:0]
	}

	for {
		select {
		case envelope, ok := <-p.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, envelope)
			if len(batch) >= p.opts.BatchSize {
				if timerArmed && !timer.Stop() {
					<-timer.C
				}
				timerArmed = false
				flush()
				continue
			}
			if !timerArmed {
				timer.Reset(p.opts.LingerFor)
				timerArmed = true
			}
		case <-timer.C:
			timerArmed = false
			flush()
		}
	}
}

func (p *AsyncPublisher) send(batch []Envelope) {
	ctx, cancel := context.WithTimeout(context.Background(), p.opts.SendTimeout)
	defer cancel()
	// The batch slice is reused by the caller, so hand the sink a copy: a sink
	// that keeps the slice (franz-go's async produce does exactly that) would
	// otherwise see its records overwritten by the next batch.
	out := make([]Envelope, len(batch))
	copy(out, batch)
	if err := p.sink.Send(ctx, out); err != nil {
		if n := p.failed.Add(uint64(len(out))); n == uint64(len(out)) || n%100 < uint64(len(out)) {
			p.logf("events: send failed for %d event(s), %d total: %v", len(out), n, err)
		}
		return
	}
	p.sent.Add(uint64(len(out)))
}

// Close stops accepting events, flushes what is buffered, and closes the sink.
//
// It is called from the server's shutdown path, where it shares the same
// deadline as the HTTP drain: a flush that cannot finish in time yields to the
// shutdown rather than holding the process open, because the events it is
// holding are, once again, a mirror of rows that are already durable.
func (p *AsyncPublisher) Close(ctx context.Context) {
	p.closeOnce.Do(func() {
		close(p.queue)
	})
	select {
	case <-p.done:
	case <-ctx.Done():
		p.logf("events: shutdown deadline reached with events still buffered (dropped=%d sent=%d)", p.Dropped(), p.Sent())
	}
	p.sink.Close()
}

// Dropped is the number of events the buffer refused. Non-zero means the sink
// could not keep up, or could not reach the broker at all.
func (p *AsyncPublisher) Dropped() uint64 { return p.dropped.Load() }

// Failed is the number of events whose batch the sink rejected.
func (p *AsyncPublisher) Failed() uint64 { return p.failed.Load() }

// Sent is the number of events the sink accepted.
func (p *AsyncPublisher) Sent() uint64 { return p.sent.Load() }

var _ Publisher = (*AsyncPublisher)(nil)
