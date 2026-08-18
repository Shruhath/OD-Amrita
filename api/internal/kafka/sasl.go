package kafka

// sasl.go — authentication mechanisms, isolated so the credential handling is
// one short readable file rather than a branch inside the dial path.

import (
	"fmt"

	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// saslMechanism maps the configured mechanism onto franz-go's.
//
// PLAIN sends the password in the clear at the protocol level, so it is safe
// only inside a TLS dial. Every hosted broker that offers PLAIN also requires
// TLS, but an operator pointing this at a self-hosted cluster could set one and
// not the other, so the combination is refused here rather than trusted to be
// caught by the broker.
func saslMechanism(cfg Config) (sasl.Mechanism, error) {
	switch cfg.SASLMechanism {
	case "":
		return nil, nil
	case "plain":
		if !cfg.TLS {
			return nil, fmt.Errorf("config: KAFKA_SASL_MECHANISM=plain sends the password unencrypted and requires KAFKA_TLS=true")
		}
		return plain.Auth{User: cfg.SASLUser, Pass: cfg.SASLPassword}.AsMechanism(), nil
	case "scram-sha-256":
		return scram.Auth{User: cfg.SASLUser, Pass: cfg.SASLPassword}.AsSha256Mechanism(), nil
	case "scram-sha-512":
		return scram.Auth{User: cfg.SASLUser, Pass: cfg.SASLPassword}.AsSha512Mechanism(), nil
	default:
		return nil, fmt.Errorf("config: unsupported KAFKA_SASL_MECHANISM %q", cfg.SASLMechanism)
	}
}
