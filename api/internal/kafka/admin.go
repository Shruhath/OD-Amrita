package kafka

// admin.go — topic provisioning.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/Shruhath/OD-Amrita/api/internal/events"
)

// TopicSpec is how one topic should be created if it does not exist.
type TopicSpec struct {
	Name              string
	Partitions        int32
	ReplicationFactor int16
	// RetentionMS is how long the broker keeps a record. Per topic, because the
	// three streams have genuinely different half-lives — see DefaultTopicSpecs.
	RetentionMS int64
}

// DefaultTopicSpecs is the intended shape of each stream.
//
// PARTITIONS: three for the two durable streams. Partitions are the unit of
// consumer parallelism and cannot be reduced later, so this is sized for "a few
// consumers eventually" rather than for today's one — while staying small
// enough that ordering-per-key remains easy to reason about. The cache topic
// gets ONE, because an invalidation broadcast has no parallelism to gain and a
// single partition makes the ordering total and therefore trivially correct.
//
// RETENTION: the audit mirror keeps 30 days, long enough for a consumer to be
// down for a weekend and catch up. The marks stream keeps 7. The cache topic
// keeps ONE HOUR — its messages are instructions that were either acted on
// within milliseconds or are worthless, and a long retention there would mean a
// restarting machine could replay a day of stale invalidations.
var DefaultTopicSpecs = []TopicSpec{
	{Name: events.TopicAudit, Partitions: 3, ReplicationFactor: 1, RetentionMS: 30 * 24 * 3600 * 1000},
	{Name: events.TopicMarks, Partitions: 3, ReplicationFactor: 1, RetentionMS: 7 * 24 * 3600 * 1000},
	{Name: events.TopicCache, Partitions: 1, ReplicationFactor: 1, RetentionMS: 3600 * 1000},
}

// EnsureTopics creates any missing topic and leaves existing ones alone.
//
// It is idempotent and safe to run on every boot, but it is NOT run on every
// boot — cmd/worker calls it, the api does not. Topic creation is a cluster-wide
// administrative act, and an api machine that autoscales should not be
// performing one; a managed cluster will usually refuse it anyway, which would
// turn a normal boot into a scary log line about a permission the process should
// never have wanted.
func EnsureTopics(ctx context.Context, cfg Config, specs []TopicSpec) error {
	if !cfg.Enabled() {
		return nil
	}
	opts, err := clientOptions(cfg)
	if err != nil {
		return err
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return fmt.Errorf("kafka: admin client: %w", err)
	}
	defer client.Close()

	admin := kadm.NewClient(client)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for _, spec := range specs {
		configs := map[string]*string{}
		if spec.RetentionMS > 0 {
			retention := fmt.Sprint(spec.RetentionMS)
			configs["retention.ms"] = &retention
		}
		responses, err := admin.CreateTopics(ctx, spec.Partitions, spec.ReplicationFactor, configs, spec.Name)
		if err != nil {
			return fmt.Errorf("kafka: create topic %s: %w", spec.Name, err)
		}
		for _, response := range responses {
			// ALREADY EXISTS is the expected answer on every boot after the first
			// and is not an error. Treating it as one would make the worker
			// crash-loop the moment it succeeded.
			if response.Err != nil && !errors.Is(response.Err, kerr.TopicAlreadyExists) {
				return fmt.Errorf("kafka: create topic %s: %w", response.Topic, response.Err)
			}
		}
	}
	return nil
}
