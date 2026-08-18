// Package kafka is the franz-go transport under internal/events: the sink that
// produces domain events, and the consumer group that reads them back.
//
// IT IS OFF UNLESS CONFIGURED. With KAFKA_BROKERS unset — the state production
// runs in until somebody deliberately changes it — Enabled() is false, no client
// is constructed, no connection is attempted, and the api process behaves in
// every observable way as it did before this package existed. That is not a
// staging convenience; it is how a new piece of infrastructure gets added to a
// system that has an event to run without putting that event at risk.
package kafka

import (
	"crypto/tls"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the resolved Kafka configuration.
type Config struct {
	// Brokers is the seed broker list. EMPTY MEANS DISABLED — this is the single
	// switch for the whole feature.
	Brokers []string
	// ClientID identifies this process to the broker, and shows up in the
	// broker's own logs and metrics. Worth setting properly: "od-amrita-api" in a
	// consumer-group listing is the difference between diagnosing a lag problem
	// and guessing at it.
	ClientID string
	// ConsumerGroup is the group id for the worker's consumers. Machines that
	// must EACH see every message (the cache bus) do not use this — see cachebus.
	ConsumerGroup string

	// TLS enables a TLS dial. Required by every hosted broker (Upstash,
	// Confluent, Redpanda Cloud); off for a local container.
	TLS bool
	// SASLMechanism is "", "plain", "scram-sha-256" or "scram-sha-512".
	SASLMechanism string
	SASLUser      string
	SASLPassword  string

	// ProduceTimeout bounds one produce round trip.
	ProduceTimeout time.Duration
	// BufferSize, BatchSize and LingerFor tune the async publisher above this
	// package. They live here so the whole stream is configured in one place.
	BufferSize int
	BatchSize  int
	LingerFor  time.Duration
}

// Enabled reports whether a broker was configured. Every construction path in
// this package checks it, so a disabled config is a no-op rather than an error:
// "not configured" is a valid, expected state, not a misconfiguration.
func (c Config) Enabled() bool { return len(c.Brokers) > 0 }

// LoadConfig reads the environment.
//
// It follows the same rule the rest of this codebase's config does: UNSET is
// valid and yields a working default, but SET-BUT-GARBAGE fails loudly. A typo
// in KAFKA_SASL_MECHANISM that silently fell back to "no authentication" would
// present as a connection that fails much later with an opaque broker error, on
// a path that is deliberately silent about its failures.
func LoadConfig() (Config, error) {
	cfg := Config{
		Brokers:       splitList(os.Getenv("KAFKA_BROKERS")),
		ClientID:      envDefault("KAFKA_CLIENT_ID", "od-amrita-api"),
		ConsumerGroup: envDefault("KAFKA_CONSUMER_GROUP", "od-amrita-worker"),
		SASLUser:      os.Getenv("KAFKA_SASL_USER"),
		SASLPassword:  os.Getenv("KAFKA_SASL_PASSWORD"),
	}
	if !cfg.Enabled() {
		return cfg, nil
	}

	useTLS, err := boolStrict("KAFKA_TLS", true)
	if err != nil {
		return Config{}, err
	}
	cfg.TLS = useTLS

	mechanism := strings.ToLower(strings.TrimSpace(os.Getenv("KAFKA_SASL_MECHANISM")))
	switch mechanism {
	case "", "none", "plain", "scram-sha-256", "scram-sha-512":
		if mechanism == "none" {
			mechanism = ""
		}
		cfg.SASLMechanism = mechanism
	default:
		return Config{}, fmt.Errorf("config: KAFKA_SASL_MECHANISM must be one of plain, scram-sha-256, scram-sha-512 or empty, got %q", mechanism)
	}
	if cfg.SASLMechanism != "" && (cfg.SASLUser == "" || cfg.SASLPassword == "") {
		return Config{}, fmt.Errorf("config: KAFKA_SASL_MECHANISM=%s requires KAFKA_SASL_USER and KAFKA_SASL_PASSWORD", cfg.SASLMechanism)
	}
	// Credentials without a mechanism is the more dangerous half of the same
	// typo: it would dial in the clear while the operator believed otherwise.
	if cfg.SASLMechanism == "" && (cfg.SASLUser != "" || cfg.SASLPassword != "") {
		return Config{}, fmt.Errorf("config: KAFKA_SASL_USER/PASSWORD are set but KAFKA_SASL_MECHANISM is not — refusing to connect unauthenticated with credentials present")
	}

	produce, err := positiveDuration("KAFKA_PRODUCE_TIMEOUT_SECONDS", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	cfg.ProduceTimeout = produce

	linger, err := positiveDuration("KAFKA_LINGER_MS", 200*time.Millisecond)
	if err != nil {
		return Config{}, err
	}
	cfg.LingerFor = linger

	buffer, err := positiveInt("KAFKA_BUFFER_SIZE", 4096)
	if err != nil {
		return Config{}, err
	}
	cfg.BufferSize = buffer

	batch, err := positiveInt("KAFKA_BATCH_SIZE", 256)
	if err != nil {
		return Config{}, err
	}
	cfg.BatchSize = batch

	return cfg, nil
}

// tlsConfig is the dial config. It sets only a minimum version and otherwise
// takes the platform defaults, so hosted brokers verify against the system root
// pool — there is deliberately no InsecureSkipVerify knob to reach for at 2am.
func (c Config) tlsConfig() *tls.Config {
	if !c.TLS {
		return nil
	}
	return &tls.Config{MinVersion: tls.VersionTLS12}
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func envDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func boolStrict(key string, def bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("config: %s=%q is not a valid boolean (use true/false)", key, raw)
	}
	return value, nil
}

func positiveInt(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("config: %s must be a positive integer, got %q", key, raw)
	}
	return value, nil
}

// positiveDuration reads a value whose UNIT IS IN THE VARIABLE NAME — _SECONDS
// or _MS — rather than a Go duration string, matching how every other timing
// setting in this codebase is expressed.
func positiveDuration(key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("config: %s must be a positive integer, got %q", key, raw)
	}
	if strings.HasSuffix(key, "_MS") {
		return time.Duration(value) * time.Millisecond, nil
	}
	return time.Duration(value) * time.Second, nil
}
