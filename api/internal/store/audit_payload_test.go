package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

func TestWithAuditRejectsMalformedPayloadBeforeDatabaseWork(t *testing.T) {
	s := New(nil)
	writeCalled := false
	err := s.WithAudit(context.Background(), AuditEvent{
		Action:  domain.ActionODCreated,
		Payload: []byte(`{"broken":`),
	}, func(context.Context, pgx.Tx) (bool, error) {
		writeCalled = true
		return true, nil
	})

	if !errors.Is(err, ErrInvalidAuditPayload) {
		t.Fatalf("error = %v, want ErrInvalidAuditPayload", err)
	}
	if writeCalled {
		t.Fatal("business callback ran for malformed audit payload")
	}
}

func TestNormalizeAuditPayload(t *testing.T) {
	for _, input := range [][]byte{nil, {}, []byte("  \n\t")} {
		got, err := normalizeAuditPayload(input)
		if err != nil {
			t.Fatalf("empty payload returned error: %v", err)
		}
		if got != nil {
			t.Errorf("empty payload = %#v, want nil for SQL NULL", got)
		}
	}

	input := []byte(`{"event_date":"2026-08-15"}`)
	got, err := normalizeAuditPayload(input)
	if err != nil {
		t.Fatalf("valid payload returned error: %v", err)
	}
	if string(got) != string(input) {
		t.Errorf("valid payload changed: got %s want %s", got, input)
	}
}
