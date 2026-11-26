// Command worker is the OD-Amrita persistent background worker entrypoint. It
// consumes the domain event stream as a CONSUMER GROUP — the partitions are
// split across however many worker instances are running, and each event is
// handled once by one of them.
//
// WHY THIS IS A SEPARATE PROCESS FROM THE API
//
//	The api process autoscales and, on Fly, is expected to stop. A consumer that
//	lived inside it would leave and rejoin its group on every scale event,
//	triggering a rebalance each time, and would stop consuming entirely whenever
//	the API scaled to nothing. Downstream work belongs in a process whose whole
//	job is to stay up.
//
//	The one consumer that DOES live in the api process is the cache bus, and it
//	is exactly the exception that proves the rule: it is groupless, it starts at
//	the end of the log, and it is invalidating that machine's OWN memory — so it
//	is meaningless anywhere else and harmless to lose when the machine goes away.
//
// With KAFKA_BROKERS unset this process prints what it would have done and
// exits 0, so a deploy that has not turned the stream on is not a crash loop.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Shruhath/OD-Amrita/api/internal/events"
	"github.com/Shruhath/OD-Amrita/api/internal/kafka"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("worker: %v", err)
	}
}

func run() error {
	cfg, err := kafka.LoadConfig()
	if err != nil {
		return err
	}
	if !cfg.Enabled() {
		fmt.Println("od-amrita worker: KAFKA_BROKERS is unset — the event stream is off, nothing to consume")
		return nil
	}
	cfg.ClientID = "od-amrita-worker"

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Topic provisioning lives HERE and not in the api, because creating a topic
	// is a cluster-wide administrative act and an autoscaling web process should
	// not be performing one. A managed cluster that forbids it returns an error,
	// which is reported and NOT fatal: the topics were then created by whoever
	// administers that cluster, and refusing to consume because we could not
	// re-create what already exists would be absurd.
	if err := kafka.EnsureTopics(ctx, cfg, kafka.DefaultTopicSpecs); err != nil {
		log.Printf("worker: topic provisioning skipped: %v", err)
	}

	consumer, err := kafka.NewConsumer(cfg, kafka.ConsumerOptions{
		Topics: []string{events.TopicAudit, events.TopicMarks},
		Group:  cfg.ConsumerGroup,
	})
	if err != nil {
		return err
	}
	defer consumer.Close()

	log.Printf("worker: consuming %v as group %q", []string{events.TopicAudit, events.TopicMarks}, cfg.ConsumerGroup)
	return consumer.Run(ctx, handle)
}

// handle is the projection seam.
//
// It LOGS and does nothing else, and that is the honest state of this process
// rather than a placeholder somebody forgot to fill in. The consumers this
// stream was built for — an export, a live board fed by push instead of by
// polling Postgres — are separate pieces of work. What exists today is the
// plumbing plus a consumer that proves end to end that events produced by the
// api are readable by another process, which is the thing that had to be true
// before any of those could be written.
//
// A handler that fails does not stop the consumer: see kafka.Handler for why a
// poison message must not be allowed to block the partition behind it.
func handle(_ context.Context, envelope events.Envelope) error {
	// Compact the payload onto one line. An audit payload is small, and a
	// consumer log where one event spans twenty lines is a consumer log nobody
	// reads during an incident.
	payload := ""
	if len(envelope.Payload) > 0 {
		var compact []byte
		if b, err := json.Marshal(json.RawMessage(envelope.Payload)); err == nil {
			compact = b
		}
		payload = string(compact)
		const maxLogged = 512
		if len(payload) > maxLogged {
			payload = payload[:maxLogged] + "…"
		}
	}
	log.Printf("worker: %s target=%s/%s actor=%s source=%s %s",
		envelope.Type, envelope.TargetTable, envelope.TargetID,
		envelope.ActorAccount, envelope.Source, payload)
	return nil
}
