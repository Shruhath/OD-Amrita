// Package eventstream assembles the domain event stream for a process: the
// publisher, the cache bus, and the broadcast consumer that applies other
// machines' invalidations.
//
// It exists so that cmd/api gains a handful of lines rather than forty, and so
// that the "everything is off when no broker is configured" decision is made in
// exactly one place instead of being re-checked at each use.
package eventstream

import (
	"context"
	"log"
	"os"

	"github.com/Shruhath/OD-Amrita/api/internal/cachebus"
	"github.com/Shruhath/OD-Amrita/api/internal/events"
	"github.com/Shruhath/OD-Amrita/api/internal/kafka"
)

// Runtime is the assembled stream. The zero value is NOT usable; New always
// returns something usable, including when the stream is disabled.
type Runtime struct {
	// Publisher is what store.WithPublisher is given. Never nil — events.Nop
	// when the stream is off — so no call site needs a nil check.
	Publisher events.Publisher
	// Cache is the invalidation bus. Never nil; with the stream off it still
	// clears local caches, which is exactly the behaviour the code has today.
	Cache *cachebus.Bus

	enabled  bool
	async    *events.AsyncPublisher
	consumer *kafka.Consumer
	cancel   context.CancelFunc
	done     chan struct{}
}

// Enabled reports whether a broker was configured.
func (r *Runtime) Enabled() bool { return r.enabled }

// New builds the stream from the environment.
//
// A CONFIGURATION ERROR IS RETURNED AND SHOULD FAIL THE BOOT, but a CONNECTION
// failure is not and does not. The distinction matters: a malformed
// KAFKA_SASL_MECHANISM is a mistake somebody just made and must be visible
// immediately, while an unreachable broker is a condition the stream is designed
// to tolerate — refusing to start the API because a mirror is unavailable would
// convert an optional dependency into a hard one, which is the single thing this
// whole design is arranged to avoid. franz-go dials lazily and reconnects, so an
// unreachable broker at boot simply means events are dropped until it returns.
func New(ctx context.Context) (*Runtime, error) {
	cfg, err := kafka.LoadConfig()
	if err != nil {
		return nil, err
	}
	source := processID()

	if !cfg.Enabled() {
		// The default path. No client, no goroutine, no connection.
		return &Runtime{
			Publisher: events.Nop{},
			Cache:     cachebus.New(nil, source, nil),
			done:      closedChan(),
		}, nil
	}

	sink, err := kafka.NewSink(cfg, source)
	if err != nil {
		return nil, err
	}
	async := events.NewAsyncPublisher(sink, events.AsyncPublisherOptions{
		BufferSize: cfg.BufferSize,
		BatchSize:  cfg.BatchSize,
		LingerFor:  cfg.LingerFor,
	})
	bus := cachebus.New(async, source, nil)

	// NO GROUP ID. Every machine must see every invalidation — see the package
	// comment on cachebus for what a group would silently do here instead.
	consumer, err := kafka.NewConsumer(cfg, kafka.ConsumerOptions{
		Topics: []string{events.TopicCache},
	})
	if err != nil {
		async.Close(ctx)
		return nil, err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	runtime := &Runtime{
		Publisher: async,
		Cache:     bus,
		enabled:   true,
		async:     async,
		consumer:  consumer,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	go func() {
		defer close(runtime.done)
		if err := consumer.Run(runCtx, bus.Handle); err != nil {
			log.Printf("eventstream: cache consumer stopped: %v", err)
		}
	}()
	log.Printf("eventstream: enabled, source=%s topics=%v", source, events.AllTopics)
	return runtime, nil
}

// Close stops the consumer and flushes buffered events, bounded by ctx.
//
// It is called from the same shutdown path as the HTTP drain and shares its
// deadline. An event still buffered when that deadline passes is dropped, which
// is correct: it mirrors a row that is already committed in Postgres, and
// holding the process open past its shutdown window to re-announce a durable
// fact is not a trade worth making.
func (r *Runtime) Close(ctx context.Context) {
	if r == nil || !r.enabled {
		return
	}
	if r.cancel != nil {
		r.cancel()
	}
	if r.consumer != nil {
		r.consumer.Close()
	}
	select {
	case <-r.done:
	case <-ctx.Done():
	}
	if r.async != nil {
		r.async.Close(ctx)
		log.Printf("eventstream: closed (sent=%d dropped=%d failed=%d)",
			r.async.Sent(), r.async.Dropped(), r.async.Failed())
	}
}

// processID identifies this process on the stream.
//
// FLY_MACHINE_ID is set by the Fly runtime and by nothing else, so it answers
// "which machine am I" without inventing a secret an operator must remember to
// set — the same reasoning the rate limiter already uses for FLY_APP_NAME. The
// hostname fallback covers local runs, where it is equally unique in practice.
func processID() string {
	if id := os.Getenv("FLY_MACHINE_ID"); id != "" {
		return id
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return "unknown"
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
