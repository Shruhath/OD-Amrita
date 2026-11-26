// Command streamctl is the operator's tool for the domain event stream.
//
// Two jobs, both of which are otherwise awkward at 2am with only rpk and a
// broker URL:
//
//	-provision   create the topics with the retention and partition counts this
//	             system actually wants, rather than whatever the broker's
//	             auto-creation defaults happen to be. Idempotent.
//	-tail        print events as they arrive, to answer "is anything actually
//	             flowing?" without writing a consumer.
//
// It reads the same KAFKA_* environment the api and the worker do, so there is
// no second place where a broker address or a credential is configured.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Shruhath/OD-Amrita/api/internal/events"
	"github.com/Shruhath/OD-Amrita/api/internal/kafka"
)

func main() {
	provision := flag.Bool("provision", false, "create the topics if they do not exist, then exit")
	tail := flag.Bool("tail", false, "print events as they arrive until interrupted")
	topics := flag.String("topics", strings.Join(events.AllTopics, ","), "comma-separated topics to tail")
	fromStart := flag.Bool("from-start", false, "tail from the beginning of each topic rather than from now")
	flag.Parse()

	if err := run(*provision, *tail, *topics, *fromStart); err != nil {
		log.Fatalf("streamctl: %v", err)
	}
}

func run(provision, tail bool, topicList string, fromStart bool) error {
	if !provision && !tail {
		flag.Usage()
		return fmt.Errorf("choose one of -provision or -tail")
	}
	cfg, err := kafka.LoadConfig()
	if err != nil {
		return err
	}
	if !cfg.Enabled() {
		return fmt.Errorf("KAFKA_BROKERS is unset — there is no stream to talk to")
	}
	cfg.ClientID = "od-amrita-streamctl"

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if provision {
		if err := kafka.EnsureTopics(ctx, cfg, kafka.DefaultTopicSpecs); err != nil {
			return err
		}
		for _, spec := range kafka.DefaultTopicSpecs {
			fmt.Printf("%-14s partitions=%d retention=%s\n",
				spec.Name, spec.Partitions, time.Duration(spec.RetentionMS)*time.Millisecond)
		}
		return nil
	}

	// NO GROUP ID. A tail is a diagnostic: it must not join the worker's consumer
	// group, because doing so would take partitions AWAY from the worker for as
	// long as somebody left the terminal open — turning a read-only look at the
	// stream into an outage of whatever the worker was doing.
	consumer, err := kafka.NewConsumer(cfg, kafka.ConsumerOptions{
		Topics:    splitTopics(topicList),
		FromStart: fromStart,
	})
	if err != nil {
		return err
	}
	defer consumer.Close()

	fmt.Fprintf(os.Stderr, "tailing %s (ctrl-c to stop)\n", topicList)
	return consumer.Run(ctx, func(_ context.Context, envelope events.Envelope) error {
		line, err := json.Marshal(envelope)
		if err != nil {
			return err
		}
		fmt.Println(string(line))
		return nil
	})
}

func splitTopics(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if topic := strings.TrimSpace(part); topic != "" {
			out = append(out, topic)
		}
	}
	return out
}
