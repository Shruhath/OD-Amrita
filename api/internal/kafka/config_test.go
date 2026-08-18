package kafka

// config_test.go — UNIT tests for the environment contract. No broker is dialled;
// LoadConfig is pure parsing, and the cases that matter are the ones where a
// mistake would otherwise surface much later as an opaque connection error.
//
// Invoked by: api-ci -> build job.

import (
	"testing"
	"time"
)

// TestLoadConfig_UnsetMeansDisabled — [P0]. This is the production configuration
// today, and the whole safety argument for merging this work rests on it: with
// no brokers, nothing is constructed and nothing is dialled.
func TestLoadConfig_UnsetMeansDisabled(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("[P0] an unset KAFKA_BROKERS must be a valid configuration, got %v", err)
	}
	if cfg.Enabled() {
		t.Error("[P0] Enabled() must be false with no brokers")
	}
}

// TestLoadConfig_MalformedMechanismFailsLoudly — [P0].
//
// The alternative — falling back to "no authentication" on a typo — is the worst
// of both: it dials in the clear, and it presents as a broker-side auth error
// far from the variable that caused it.
func TestLoadConfig_MalformedMechanismFailsLoudly(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "broker:9092")
	t.Setenv("KAFKA_SASL_MECHANISM", "scram-sha-1")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("[P0] an unsupported SASL mechanism must fail the boot, not fall back to unauthenticated")
	}
}

// TestLoadConfig_CredentialsWithoutAMechanismAreRefused — [P0].
//
// The dangerous half of the same typo: the operator set a user and password,
// believes the connection is authenticated, and it is not.
func TestLoadConfig_CredentialsWithoutAMechanismAreRefused(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "broker:9092")
	t.Setenv("KAFKA_SASL_MECHANISM", "")
	t.Setenv("KAFKA_SASL_USER", "od")
	t.Setenv("KAFKA_SASL_PASSWORD", "secret")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("[P0] credentials present with no mechanism must be refused rather than silently ignored")
	}
}

// TestLoadConfig_MechanismWithoutCredentialsIsRefused — [P1].
func TestLoadConfig_MechanismWithoutCredentialsIsRefused(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "broker:9092")
	t.Setenv("KAFKA_SASL_MECHANISM", "scram-sha-256")
	t.Setenv("KAFKA_SASL_USER", "")
	t.Setenv("KAFKA_SASL_PASSWORD", "")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("[P1] a mechanism with no credentials must be refused")
	}
}

// TestSASL_PlainRequiresTLS — [P0]. PLAIN sends the password unencrypted at the
// protocol level. A self-hosted cluster would happily accept it.
func TestSASL_PlainRequiresTLS(t *testing.T) {
	cfg := Config{
		Brokers:       []string{"broker:9092"},
		SASLMechanism: "plain",
		SASLUser:      "od",
		SASLPassword:  "secret",
		TLS:           false,
	}
	if _, err := saslMechanism(cfg); err == nil {
		t.Fatal("[P0] SASL PLAIN without TLS must be refused — it would send the password in the clear")
	}

	cfg.TLS = true
	if _, err := saslMechanism(cfg); err != nil {
		t.Fatalf("[P0] SASL PLAIN over TLS must be accepted, got %v", err)
	}
}

// TestLoadConfig_TLSDefaultsOn — [P1]. Every hosted broker requires TLS, so the
// default that costs nothing to get wrong is the secure one; a local container
// is the case that opts out.
func TestLoadConfig_TLSDefaultsOn(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "broker:9092")
	t.Setenv("KAFKA_TLS", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TLS {
		t.Error("[P1] TLS must default to on")
	}
	if cfg.tlsConfig().MinVersion != 0x0303 {
		t.Error("[P1] the dial config must floor at TLS 1.2")
	}
}

// TestLoadConfig_UnitsComeFromTheVariableName — [P2]. _MS is milliseconds and
// _SECONDS is seconds, matching every other timing setting in this codebase; a
// silent unit mismatch would make the linger three orders of magnitude wrong.
func TestLoadConfig_UnitsComeFromTheVariableName(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "broker:9092")
	t.Setenv("KAFKA_LINGER_MS", "250")
	t.Setenv("KAFKA_PRODUCE_TIMEOUT_SECONDS", "5")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LingerFor != 250*time.Millisecond {
		t.Errorf("[P2] LingerFor = %v, want 250ms", cfg.LingerFor)
	}
	if cfg.ProduceTimeout != 5*time.Second {
		t.Errorf("[P2] ProduceTimeout = %v, want 5s", cfg.ProduceTimeout)
	}
}

// TestLoadConfig_GarbageNumbersFailRatherThanDefault — [P1]. Same rule the rest
// of this codebase's config follows: unset is valid, set-but-wrong is loud.
func TestLoadConfig_GarbageNumbersFailRatherThanDefault(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "broker:9092")
	t.Setenv("KAFKA_BATCH_SIZE", "lots")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("[P1] a malformed KAFKA_BATCH_SIZE must fail rather than silently use the default")
	}
}

// TestLoadConfig_BrokerListIsSplitAndTrimmed — [P2].
func TestLoadConfig_BrokerListIsSplitAndTrimmed(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", " a:9092 , b:9092 ,, ")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a:9092", "b:9092"}
	if len(cfg.Brokers) != len(want) {
		t.Fatalf("[P2] Brokers = %v, want %v", cfg.Brokers, want)
	}
	for i := range want {
		if cfg.Brokers[i] != want[i] {
			t.Errorf("[P2] Brokers[%d] = %q, want %q", i, cfg.Brokers[i], want[i])
		}
	}
}

// TestDefaultTopicSpecs_CacheTopicRetentionIsShort — [P1].
//
// The cache topic is consumed from the END of the log by every machine, but a
// long retention there is still wrong: it makes a topic full of stale
// instructions that a mis-set offset could replay, and it stores instructions
// that were worthless milliseconds after they were sent.
func TestDefaultTopicSpecs_CacheTopicRetentionIsShort(t *testing.T) {
	const oneDayMS = int64(24 * 3600 * 1000)
	for _, spec := range DefaultTopicSpecs {
		if spec.Name != "od.cache.v1" {
			continue
		}
		if spec.RetentionMS >= oneDayMS {
			t.Errorf("[P1] cache topic retention = %dms, want well under a day", spec.RetentionMS)
		}
		if spec.Partitions != 1 {
			t.Errorf("[P1] cache topic partitions = %d, want 1 so invalidation ordering is total", spec.Partitions)
		}
		return
	}
	t.Fatal("[P1] the cache topic has no provisioning spec")
}

// TestDefaultTopicSpecs_CoversEveryProducedTopic — [P1]. A produced-but-
// unprovisioned topic falls back to auto-creation and the broker's default
// retention, which for the cache topic is badly wrong.
func TestDefaultTopicSpecs_CoversEveryProducedTopic(t *testing.T) {
	specced := map[string]bool{}
	for _, spec := range DefaultTopicSpecs {
		specced[spec.Name] = true
	}
	for _, topic := range []string{"od.audit.v1", "od.marks.v1", "od.cache.v1"} {
		if !specced[topic] {
			t.Errorf("[P1] %q is produced to but has no provisioning spec", topic)
		}
	}
}
