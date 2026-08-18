package kafka

// sink.go — the producer. It implements events.Sink, so everything above it
// (buffering, batching, drop-on-full) is transport-free and tested without a
// broker.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/Shruhath/OD-Amrita/api/internal/events"
)

// Sink produces envelopes to Kafka.
type Sink struct {
	client *kgo.Client
	source string
}

// NewSink dials the brokers and returns a producing sink.
//
// source is stamped onto every envelope that does not already carry one; it is
// the Fly machine id in production. It is not cosmetic — the cache bus uses it
// to ignore its own broadcasts.
func NewSink(cfg Config, source string) (*Sink, error) {
	if !cfg.Enabled() {
		return nil, fmt.Errorf("kafka: no brokers configured")
	}
	opts, err := clientOptions(cfg)
	if err != nil {
		return nil, err
	}
	opts = append(opts,
		// Idempotent produce is franz-go's default and is left on deliberately:
		// it removes the duplicate a retried produce would otherwise create, which
		// is the difference between a consumer needing to deduplicate and a
		// consumer merely needing to be correct.
		kgo.ProducerBatchCompression(kgo.Lz4Compression(), kgo.SnappyCompression(), kgo.NoCompression()),
		kgo.ProduceRequestTimeout(cfg.ProduceTimeout),
		// The broker is asked to create a topic that does not exist yet. Real
		// clusters usually disable that server-side, which is fine — see
		// EnsureTopics, which is the explicit path. This just means a local
		// Redpanda works with no setup step.
		kgo.AllowAutoTopicCreation(),
	)
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: client: %w", err)
	}
	return &Sink{client: client, source: source}, nil
}

// Send produces every envelope to every topic its type routes to.
//
// It uses the ASYNC produce plus an explicit flush rather than ProduceSync, so
// the whole batch is in flight at once instead of one record at a time. On a
// batch of a hundred marks that is one round trip rather than a hundred.
func (s *Sink) Send(ctx context.Context, envelopes []events.Envelope) error {
	if len(envelopes) == 0 {
		return nil
	}
	records := make([]*kgo.Record, 0, len(envelopes))
	for _, envelope := range envelopes {
		if envelope.Source == "" {
			envelope.Source = s.source
		}
		value, err := json.Marshal(envelope)
		if err != nil {
			// A malformed envelope must not take its batch down with it: the other
			// events in this batch are unrelated facts about other students.
			continue
		}
		for _, topic := range events.TopicsFor(envelope.Type) {
			records = append(records, &kgo.Record{
				Topic: topic,
				Key:   []byte(envelope.Key()),
				Value: value,
				Headers: []kgo.RecordHeader{
					{Key: "event_type", Value: []byte(envelope.Type)},
					{Key: "schema_version", Value: []byte(fmt.Sprint(envelope.SchemaVersion))},
				},
			})
		}
	}
	if len(records) == 0 {
		return nil
	}

	var firstErr error
	results := s.client.ProduceSync(ctx, records...)
	for _, result := range results {
		if result.Err != nil && firstErr == nil {
			firstErr = result.Err
		}
	}
	if firstErr != nil {
		return fmt.Errorf("kafka: produce: %w", firstErr)
	}
	return nil
}

// Close flushes and shuts the client down. franz-go's Close flushes first, so a
// record accepted by Send has been given its chance to land.
func (s *Sink) Close() {
	if s.client != nil {
		s.client.Close()
	}
}

var _ events.Sink = (*Sink)(nil)

// clientOptions builds the dial options shared by the sink and the consumer.
func clientOptions(cfg Config) ([]kgo.Opt, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(cfg.ClientID),
	}
	if tlsCfg := cfg.tlsConfig(); tlsCfg != nil {
		opts = append(opts, kgo.DialTLSConfig(tlsCfg))
	}
	mechanism, err := saslMechanism(cfg)
	if err != nil {
		return nil, err
	}
	if mechanism != nil {
		opts = append(opts, kgo.SASL(mechanism))
	}
	return opts, nil
}
