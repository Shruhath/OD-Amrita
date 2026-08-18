package kafka

// consumer.go — reading the stream back. Two shapes, because this system needs
// both and they are NOT the same thing:
//
//   - a CONSUMER GROUP, where the partitions are split across the members and
//     each message is handled once by one of them. That is what work looks like:
//     an archival sink, an export, a projection.
//   - a BROADCAST consumer, where every process reads every message. That is what
//     a NOTIFICATION looks like: the cache bus, where the point is precisely that
//     all machines hear it.
//
// Using a group where a broadcast was meant is a silent bug: with two Fly
// machines in a group, each invalidation reaches one of them, so half the fleet
// keeps serving stale answers and the symptom is intermittent by machine.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/Shruhath/OD-Amrita/api/internal/events"
)

// Handler processes one envelope.
//
// Returning an error does NOT stop the consumer and does not prevent the offset
// commit. That is deliberate for this system's traffic: a poison message that
// blocked the partition would stop every later event behind it, and these events
// are a mirror — falling permanently behind is worse than skipping one record
// and logging it.
type Handler func(ctx context.Context, envelope events.Envelope) error

// ConsumerOptions configures a consumer.
type ConsumerOptions struct {
	// Topics to read.
	Topics []string
	// Group is the consumer group id. EMPTY MEANS BROADCAST: the consumer joins
	// no group, reads every partition itself, and starts at the END of the log.
	Group string
	// FromStart reads the topic from its beginning rather than its end. Only
	// meaningful for a group consumer with no committed offsets; a broadcast
	// consumer always starts at the end, because a notification that was sent
	// before this process existed is not news.
	FromStart bool
	Logf      func(format string, args ...any)
}

// Consumer reads envelopes and hands them to a Handler.
type Consumer struct {
	client *kgo.Client
	logf   func(string, ...any)
}

// NewConsumer dials the brokers and subscribes.
func NewConsumer(cfg Config, opts ConsumerOptions) (*Consumer, error) {
	if !cfg.Enabled() {
		return nil, fmt.Errorf("kafka: no brokers configured")
	}
	if len(opts.Topics) == 0 {
		return nil, fmt.Errorf("kafka: consumer needs at least one topic")
	}
	clientOpts, err := clientOptions(cfg)
	if err != nil {
		return nil, err
	}
	clientOpts = append(clientOpts, kgo.ConsumeTopics(opts.Topics...))
	if opts.Group != "" {
		clientOpts = append(clientOpts,
			kgo.ConsumerGroup(opts.Group),
			// Offsets are committed on a timer by franz-go's default. At-least-once
			// delivery is the resulting contract, and it is the right one: a
			// duplicate projection is recoverable, a lost one is not.
			kgo.DisableAutoCommit(),
		)
		if opts.FromStart {
			clientOpts = append(clientOpts, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
		}
	} else {
		// Broadcast: no group, and start at the end. See ConsumerOptions.FromStart.
		clientOpts = append(clientOpts, kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()))
	}
	client, err := kgo.NewClient(clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: consumer client: %w", err)
	}
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}
	return &Consumer{client: client, logf: logf}, nil
}

// Run polls until ctx is cancelled. It blocks, and returns nil on a clean
// cancellation so a caller can treat "the context ended" as success rather than
// having to unwrap a context error at every call site.
func (c *Consumer) Run(ctx context.Context, handle Handler) error {
	for {
		fetches := c.client.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return nil
		}
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		// Fetch errors are per topic/partition and are usually transient
		// (a rebalance, a broker restart). Log and keep polling — franz-go
		// retries the underlying request itself.
		fetches.EachError(func(topic string, partition int32, err error) {
			c.logf("kafka: fetch error on %s[%d]: %v", topic, partition, err)
		})

		fetches.EachRecord(func(record *kgo.Record) {
			var envelope events.Envelope
			if err := json.Unmarshal(record.Value, &envelope); err != nil {
				c.logf("kafka: undecodable record on %s[%d]@%d: %v", record.Topic, record.Partition, record.Offset, err)
				return
			}
			// A record from a FUTURE schema version is skipped rather than
			// half-parsed. json.Unmarshal would happily fill in the fields it
			// recognises and silently drop the rest, which is the failure mode
			// where a consumer acts on an event it only partly understood.
			if envelope.SchemaVersion > events.SchemaVersion {
				c.logf("kafka: skipping %s at schema v%d (this build understands v%d)", envelope.Type, envelope.SchemaVersion, events.SchemaVersion)
				return
			}
			if err := handle(ctx, envelope); err != nil {
				c.logf("kafka: handler failed for %s (%s): %v", envelope.Type, envelope.ID, err)
			}
		})

		// Commit AFTER the batch is handled, so a crash mid-batch replays it
		// rather than skipping it. Only a group consumer has offsets to commit.
		if err := c.client.CommitUncommittedOffsets(ctx); err != nil && ctx.Err() == nil {
			c.logf("kafka: offset commit: %v", err)
		}
	}
}

// Close stops the consumer and leaves the group.
func (c *Consumer) Close() {
	if c.client != nil {
		c.client.Close()
	}
}
