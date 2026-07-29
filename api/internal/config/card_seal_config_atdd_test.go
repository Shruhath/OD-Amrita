package config

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadCardSealConfigAcceptsOnlyServerSideVersionedStrongKeys(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	setEnv(t, map[string]string{
		"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
		"TLS_ENABLED":          "false",
	})
	t.Setenv("CARD_SEAL_ACTIVE_VERSION", "v1")
	t.Setenv("CARD_SEAL_HMAC_KEYS", "v1="+base64.RawURLEncoding.EncodeToString(key))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CardSeal.ActiveVersion != "v1" {
		t.Fatalf("active version = %q, want v1", cfg.CardSeal.ActiveVersion)
	}
	if got := cfg.CardSeal.HMACKeys["v1"]; !bytes.Equal(got, key) {
		t.Fatalf("decoded key = %x, want configured key", got)
	}
}

func TestLoadCardSealConfigFailsFastForMissingMalformedOrShortKeys(t *testing.T) {
	for _, tc := range []struct {
		name    string
		active  string
		keys    string
		wantErr string
	}{
		{name: "blank active version", active: "", keys: validCardSealKey("v1", 0x01), wantErr: "CARD_SEAL_ACTIVE_VERSION"},
		{name: "missing active key", active: "v2", keys: validCardSealKey("v1", 0x01), wantErr: "CARD_SEAL_HMAC_KEYS"},
		{name: "overlong active version", active: "version-name-over-16", keys: validCardSealKey("v1", 0x01), wantErr: "CARD_SEAL_ACTIVE_VERSION"},
		{name: "overlong configured version", active: "v1", keys: validCardSealKey("version-name-over-16", 0x01), wantErr: "16 characters"},
		{name: "malformed base64", active: "v1", keys: "v1=not+base64", wantErr: "CARD_SEAL_HMAC_KEYS"},
		{name: "short decoded key", active: "v1", keys: "v1=" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, 31)), wantErr: "32 bytes"},
		{name: "duplicate version", active: "v1", keys: validCardSealKey("v1", 0x01) + "," + validCardSealKey("v1", 0x02), wantErr: "duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, map[string]string{
				"DATABASE_URL_RUNTIME": "postgres://od_runtime:x@localhost:5432/od_amrita",
				"TLS_ENABLED":          "false",
			})
			t.Setenv("CARD_SEAL_ACTIVE_VERSION", tc.active)
			t.Setenv("CARD_SEAL_HMAC_KEYS", tc.keys)

			_, err := Load()
			if err == nil {
				t.Fatal("expected Load to reject invalid card seal config")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), tc.keys) {
				t.Fatalf("error leaked card seal key material: %v", err)
			}
		})
	}
}

func validCardSealKey(version string, fill byte) string {
	return version + "=" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}
