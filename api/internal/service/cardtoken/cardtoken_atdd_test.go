package cardtoken

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

func TestSealDeterministicPayloadAndLedgerIdempotency(t *testing.T) {
	ctx := context.Background()
	student := Student{
		ID:          "97000000-0000-4000-8000-000000000001",
		RollNo:      "CB.SC.U4CSE99001",
		DisplayName: "Ananya Rao",
		Branch:      "CSE",
		Section:     "A",
		Batch:       "2024",
	}
	ledger := &fakeLedger{}
	audit := &fakeAuditStore{}
	keyring := mustKeyring(t, "v1", map[string][]byte{"v1": bytes.Repeat([]byte{0x42}, 32)})
	svc := NewService(fakeReader{student: student}, ledger, audit, keyring)

	first, err := svc.Seal(ctx, adminActor(), SealInput{StudentID: student.ID})
	if err != nil {
		t.Fatalf("first seal: %v", err)
	}
	second, err := svc.Seal(ctx, adminActor(), SealInput{StudentID: student.ID})
	if err != nil {
		t.Fatalf("second seal: %v", err)
	}

	if first.Version != "v1" || second.Version != "v1" {
		t.Fatalf("version mismatch: first=%q second=%q", first.Version, second.Version)
	}
	if first.Token == "" || first.Token != second.Token {
		t.Fatalf("token must be deterministic and non-empty: first=%q second=%q", first.Token, second.Token)
	}
	if len(first.Token) != 43 {
		t.Fatalf("HMAC-SHA256 base64url token length = %d, want 43", len(first.Token))
	}
	if first.Payload.Version != "v1" || first.Payload.Token != first.Token {
		t.Fatalf("payload = %+v, want only active version and token", first.Payload)
	}
	if first.PayloadContainsStudentIdentity() {
		t.Fatalf("card payload must not contain student identity: %+v", first.Payload)
	}
	if ledger.upserts != 2 || ledger.createdRows != 1 {
		t.Fatalf("ledger upserts=%d createdRows=%d, want two attempts and one active row", ledger.upserts, ledger.createdRows)
	}
	if ledger.lastVersion != "v1" {
		t.Fatalf("ledger version = %q, want v1", ledger.lastVersion)
	}
	wantHash := sha256.Sum256([]byte(first.Token))
	if ledger.lastHash != wantHash {
		t.Fatalf("ledger hash = %x, want SHA256(token) %x", ledger.lastHash, wantHash)
	}
	if audit.changedWrites != 1 {
		t.Fatalf("audit changed writes=%d, want only first seal audited", audit.changedWrites)
	}
	if bytes.Contains(audit.payload, []byte(first.Token)) || bytes.Contains(audit.payload, []byte("CB.SC.U4CSE99001")) {
		t.Fatalf("audit payload leaked raw token or roll number: %s", audit.payload)
	}
}

func TestSealUsesActiveVersionAndKeyMaterialChangesToken(t *testing.T) {
	ctx := context.Background()
	student := Student{ID: "97000000-0000-4000-8000-000000000002", RollNo: "CB.SC.U4CSE99002"}
	v1 := NewService(fakeReader{student: student}, &fakeLedger{}, &fakeAuditStore{}, mustKeyring(t, "v1", map[string][]byte{"v1": bytes.Repeat([]byte{0x11}, 32)}))
	v2 := NewService(fakeReader{student: student}, &fakeLedger{}, &fakeAuditStore{}, mustKeyring(t, "v2", map[string][]byte{"v2": bytes.Repeat([]byte{0x22}, 32)}))

	first, err := v1.Seal(ctx, adminActor(), SealInput{StudentID: student.ID})
	if err != nil {
		t.Fatalf("seal v1: %v", err)
	}
	second, err := v2.Seal(ctx, adminActor(), SealInput{StudentID: student.ID})
	if err != nil {
		t.Fatalf("seal v2: %v", err)
	}
	if first.Token == second.Token {
		t.Fatal("different active key/version must produce a different token")
	}
	if first.Version != "v1" || second.Version != "v2" {
		t.Fatalf("versions = %q/%q, want v1/v2", first.Version, second.Version)
	}
}

func TestResolveKnownTokenAndRejectsUniformFailures(t *testing.T) {
	ctx := context.Background()
	student := Student{
		ID:          "97000000-0000-4000-8000-000000000003",
		RollNo:      "CB.SC.U4CSE99003",
		DisplayName: "Madhav Rao",
		Branch:      "CSE",
		Section:     "B",
		Batch:       "2024",
	}
	ledger := &fakeLedger{resolved: student}
	audit := &fakeAuditStore{}
	svc := NewService(fakeReader{student: student}, ledger, audit, mustKeyring(t, "v1", map[string][]byte{"v1": bytes.Repeat([]byte{0x33}, 32)}))
	sealed, err := svc.Seal(ctx, adminActor(), SealInput{StudentID: student.ID})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	resolved, err := svc.Resolve(ctx, adminActor(), ResolveInput{Version: sealed.Version, Token: sealed.Token})
	if err != nil {
		t.Fatalf("resolve known token: %v", err)
	}
	if resolved.StudentID != student.ID || resolved.RollNo != student.RollNo || resolved.DisplayName != student.DisplayName {
		t.Fatalf("resolved = %+v, want %+v", resolved, student)
	}
	if ledger.resolveVersion != sealed.Version {
		t.Fatalf("resolved version = %q, want %q", ledger.resolveVersion, sealed.Version)
	}
	if want := sha256.Sum256([]byte(sealed.Token)); ledger.resolveHash != want {
		t.Fatalf("resolved hash = %x, want SHA256(token) %x", ledger.resolveHash, want)
	}
	if audit.changedWrites != 2 || audit.actions[1] != domain.ActionCardTokenVerified {
		t.Fatalf("audit writes/actions = %d/%v, want seal plus card_token.verified", audit.changedWrites, audit.actions)
	}
	if bytes.Contains(audit.payload, []byte(sealed.Token)) || bytes.Contains(audit.payload, []byte(student.RollNo)) {
		t.Fatalf("verification audit payload leaked raw token or roll number: %s", audit.payload)
	}

	for _, tc := range []ResolveInput{
		{Version: "v1", Token: "not-base64url"},
		{Version: "disabled", Token: sealed.Token},
		{Version: "v1", Token: sealed.Token[:len(sealed.Token)-1] + "A"},
	} {
		_, err := svc.Resolve(ctx, adminActor(), tc)
		var de domain.DomainError
		if !errors.As(err, &de) || de.Code != contracts.ErrorCodeCARDNOTRECOGNIZED {
			t.Fatalf("Resolve(%+v) error = %v, want CARD_NOT_RECOGNIZED", tc, err)
		}
	}
}

func TestNonAdminCannotSealOrResolve(t *testing.T) {
	svc := NewService(fakeReader{}, &fakeLedger{}, &fakeAuditStore{}, mustKeyring(t, "v1", map[string][]byte{"v1": bytes.Repeat([]byte{0x44}, 32)}))

	for _, err := range []error{
		func() error {
			_, err := svc.Seal(context.Background(), Actor{Role: "teacher"}, SealInput{StudentID: "97000000-0000-4000-8000-000000000004"})
			return err
		}(),
		func() error {
			_, err := svc.Resolve(context.Background(), Actor{Role: "student"}, ResolveInput{Version: "v1", Token: "abc"})
			return err
		}(),
	} {
		var de domain.DomainError
		if !errors.As(err, &de) || de.Code != contracts.ErrorCodeRBACFORBIDDEN {
			t.Fatalf("error = %v, want RBAC_FORBIDDEN", err)
		}
	}
}

func mustKeyring(t *testing.T, active string, keys map[string][]byte) Keyring {
	t.Helper()
	keyring, err := NewKeyring(active, keys)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return keyring
}

func adminActor() Actor {
	return Actor{UserID: "97000000-0000-4000-8000-000000000099", Email: "admin@amrita.edu", Role: "admin"}
}

type fakeReader struct {
	student Student
	err     error
}

func (f fakeReader) StudentByID(context.Context, string) (Student, error) {
	if f.err != nil {
		return Student{}, f.err
	}
	return f.student, nil
}

type fakeLedger struct {
	upserts        int
	createdRows    int
	lastVersion    string
	lastHash       [sha256.Size]byte
	resolveVersion string
	resolveHash    [sha256.Size]byte
	resolved       Student
	// verifiedFor records every MarkVerified call, so a test can assert that a
	// populator read-back is remembered — and that a REFUSED read is not.
	verifiedFor []string
	sectionFor  []SectionFilter
	sectionRows []CardStatus
}

func (f *fakeLedger) MarkVerified(_ context.Context, _ pgx.Tx, version string, _ [sha256.Size]byte, actorUserID string) (bool, error) {
	f.verifiedFor = append(f.verifiedFor, version+"/"+actorUserID)
	return true, nil
}

func (f *fakeLedger) SectionStatus(_ context.Context, _ string, filter SectionFilter) ([]CardStatus, error) {
	f.sectionFor = append(f.sectionFor, filter)
	return f.sectionRows, nil
}

func (f *fakeLedger) Upsert(_ context.Context, _ pgx.Tx, entry LedgerEntry) (bool, error) {
	f.upserts++
	f.lastVersion = entry.Version
	f.lastHash = entry.TokenSHA256
	if f.upserts == 1 {
		f.createdRows++
		return true, nil
	}
	return false, nil
}

func (f *fakeLedger) Resolve(_ context.Context, version string, fingerprint [sha256.Size]byte) (Student, error) {
	f.resolveVersion = version
	f.resolveHash = fingerprint
	if f.resolved.ID == "" {
		return Student{}, ErrCardNotRecognized
	}
	return f.resolved, nil
}

type fakeAuditStore struct {
	changedWrites int
	payload       []byte
	actions       []domain.Action
}

func (f *fakeAuditStore) WithAudit(ctx context.Context, evt store.AuditEvent, write func(context.Context, pgx.Tx) (bool, error)) error {
	changed, err := write(ctx, nil)
	if err != nil {
		return err
	}
	if changed {
		f.changedWrites++
		f.actions = append(f.actions, evt.Action)
		if evt.PayloadFunc != nil {
			payload, err := evt.PayloadFunc()
			if err != nil {
				return err
			}
			f.payload = payload
		} else {
			f.payload = evt.Payload
		}
		if !json.Valid(f.payload) {
			return errors.New("invalid audit payload")
		}
	}
	return nil
}
