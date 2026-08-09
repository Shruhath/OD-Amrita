package cardtoken

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	"github.com/Shruhath/OD-Amrita/api/internal/store"
)

var (
	ErrStudentNotFound   = errors.New("cardtoken: student not found")
	ErrCardNotRecognized = errors.New("cardtoken: card not recognized")
)

type Actor struct {
	UserID string
	Email  string
	Role   string
}

type Student struct {
	ID          string
	RollNo      string
	DisplayName string
	Branch      string
	Section     string
	Batch       string
}

type ResolvedStudent struct {
	StudentID   string
	RollNo      string
	DisplayName string
	Branch      string
	Section     string
	Batch       string
}

type CardPayload struct {
	Version string `json:"version"`
	Token   string `json:"token"`
}

func (p CardPayload) PayloadContainsStudentIdentity() bool {
	encoded, _ := json.Marshal(p)
	for _, forbidden := range []string{"roll_no", "display_name", "student_id", "branch", "section", "batch", "email", "key"} {
		if strings.Contains(string(encoded), forbidden) {
			return true
		}
	}
	return false
}

type SealInput struct {
	StudentID string
}

type ResolveInput struct {
	Version string
	Token   string
}

type SealResult struct {
	Student ResolvedStudent
	Version string
	Token   string
	Payload CardPayload
}

func (r SealResult) PayloadContainsStudentIdentity() bool {
	return r.Payload.PayloadContainsStudentIdentity()
}

type LedgerEntry struct {
	StudentID    uuid.UUID
	Version      string
	TokenSHA256  [sha256.Size]byte
	StudentRoll  string
	Fingerprint8 string
}

type Keyring struct {
	active string
	keys   map[string][]byte
}

func NewKeyring(active string, keys map[string][]byte) (Keyring, error) {
	active = strings.TrimSpace(active)
	if active == "" {
		return Keyring{}, errors.New("cardtoken: active seal version is required")
	}
	copied := make(map[string][]byte, len(keys))
	for version, key := range keys {
		version = strings.TrimSpace(version)
		if version == "" {
			return Keyring{}, errors.New("cardtoken: seal key version is required")
		}
		if len(version) > 16 {
			return Keyring{}, fmt.Errorf("cardtoken: seal key version %q must not exceed 16 characters", version)
		}
		if len(key) < 32 {
			return Keyring{}, fmt.Errorf("cardtoken: seal key for version %q must be at least 32 bytes", version)
		}
		copied[version] = append([]byte(nil), key...)
	}
	if _, ok := copied[active]; !ok {
		return Keyring{}, fmt.Errorf("cardtoken: active seal version %q has no configured key", active)
	}
	return Keyring{active: active, keys: copied}, nil
}

func (k Keyring) ActiveVersion() string {
	return k.active
}

func (k Keyring) key(version string) ([]byte, bool) {
	key, ok := k.keys[version]
	return key, ok
}

type Reader interface {
	StudentByID(context.Context, string) (Student, error)
}

type Ledger interface {
	Upsert(context.Context, pgx.Tx, LedgerEntry) (bool, error)
	Resolve(context.Context, string, [sha256.Size]byte) (Student, error)
	// MarkVerified records that an admin read this card back. It takes the
	// fingerprint that was just resolved rather than a student id, so it can only
	// mark the row the read actually matched.
	MarkVerified(ctx context.Context, version string, fingerprint [sha256.Size]byte, actorUserID string) error
	// SectionStatus is the populator's DURABLE progress for one section, for one
	// seal version. The version is passed rather than held: the service owns the
	// keyring, and a ledger configured separately is one somebody forgets to
	// configure.
	SectionStatus(ctx context.Context, version string, filter SectionFilter) ([]CardStatus, error)
}

// SectionFilter names one branch/batch/section — the unit the populator works
// through, and therefore the unit its counts are about.
type SectionFilter struct {
	BranchID string
	Batch    string
	Section  string
}

// CardStatus is what the server knows about one student's card. It carries no
// token, no fingerprint and no key material: this is a progress read, and the
// populator has no use for card material it is not about to write.
type CardStatus struct {
	StudentID string
	// Issued means a token row exists. It is NOT the same as written — a seal
	// can be handed out and the write can still fail on the card.
	Issued bool
	// VerifiedAt is set only once an admin read the card back and the server
	// recognised it. This is the one that may be reported as done.
	VerifiedAt *time.Time
}

type AuditStore interface {
	WithAudit(context.Context, store.AuditEvent, func(context.Context, pgx.Tx) (bool, error)) error
}

type Service struct {
	reader  Reader
	ledger  Ledger
	audited AuditStore
	keyring Keyring
}

func NewService(reader Reader, ledger Ledger, audited AuditStore, keyring Keyring) *Service {
	return &Service{reader: reader, ledger: ledger, audited: audited, keyring: keyring}
}

func (s *Service) Seal(ctx context.Context, actor Actor, input SealInput) (SealResult, error) {
	if actor.Role != "admin" {
		return SealResult{}, forbidden()
	}
	studentID, err := uuid.Parse(strings.TrimSpace(input.StudentID))
	if err != nil {
		return SealResult{}, validation("student_id", "invalid_format", "Student id must be a UUID.")
	}
	student, err := s.reader.StudentByID(ctx, studentID.String())
	if errors.Is(err, ErrStudentNotFound) {
		return SealResult{}, studentNotFound()
	}
	if err != nil {
		return SealResult{}, err
	}
	version := s.keyring.ActiveVersion()
	token, err := s.token(version, student.RollNo)
	if err != nil {
		return SealResult{}, err
	}
	fingerprint := sha256.Sum256([]byte(token))
	entry := LedgerEntry{
		StudentID:    studentID,
		Version:      version,
		TokenSHA256:  fingerprint,
		StudentRoll:  student.RollNo,
		Fingerprint8: fmt.Sprintf("%x", fingerprint[:4]),
	}

	actorAccount := strings.ToLower(strings.TrimSpace(actor.Email))
	actorRole := actor.Role
	targetTable := "student_card_tokens"
	var targetID string
	evt := store.AuditEvent{
		Action:       domain.ActionCardTokenSealed,
		ActorAccount: stringPtrOrNil(actorAccount),
		ActorRole:    stringPtrOrNil(actorRole),
		TargetTable:  &targetTable,
		TargetID:     &targetID,
	}
	evt.PayloadFunc = func() ([]byte, error) {
		return json.Marshal(map[string]any{
			"student_id":               studentID.String(),
			"version":                  version,
			"token_fingerprint_prefix": entry.Fingerprint8,
		})
	}
	err = s.audited.WithAudit(ctx, evt, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		changed, writeErr := s.ledger.Upsert(ctx, tx, entry)
		if writeErr != nil {
			return false, writeErr
		}
		targetID = studentID.String()
		return changed, nil
	})
	if err != nil {
		return SealResult{}, err
	}
	return SealResult{
		Student: resolved(student),
		Version: version,
		Token:   token,
		Payload: CardPayload{Version: version, Token: token},
	}, nil
}

// Resolve is the POPULATOR's read-back. Recognising the card is what "verified"
// means, so a successful admin resolve records the fact.
//
// The recording is best-effort on purpose: the operator is holding a phone
// against a card, and the answer they need — whose card is this — has already
// been computed. Failing the read because the progress note could not be saved
// would turn a working verification into an error and send them round again. A
// missed write shows as "issued" and costs one extra tap; a failed read costs
// the same tap plus the operator's confidence.
func (s *Service) Resolve(ctx context.Context, actor Actor, input ResolveInput) (ResolvedStudent, error) {
	if actor.Role != "admin" {
		return ResolvedStudent{}, forbidden()
	}
	student, err := s.ResolveToken(ctx, input)
	if err != nil {
		return ResolvedStudent{}, err
	}
	if actorID := strings.TrimSpace(actor.UserID); actorID != "" {
		fingerprint := sha256.Sum256([]byte(strings.TrimSpace(input.Token)))
		_ = s.ledger.MarkVerified(ctx, strings.TrimSpace(input.Version), fingerprint, actorID)
	}
	return student, nil
}

// SectionStatus answers "how far has this section actually got" from the
// database rather than from the operator's browser.
//
// WHY IT EXISTS. The populator's progress lived only in sessionStorage. It did
// not survive signing out — which clears persisted state deliberately, because a
// shared phone must not hand the next operator the previous one's screen — and
// it never crossed devices. An admin who wrote and verified a section, signed
// out, and came back was told 0 of 0 were verified with every row reading "Not
// written", and had no way to tell which cards were already done.
func (s *Service) SectionStatus(ctx context.Context, actor Actor, filter SectionFilter) ([]CardStatus, error) {
	if actor.Role != "admin" {
		return nil, forbidden()
	}
	filter.BranchID = strings.TrimSpace(filter.BranchID)
	filter.Batch = strings.TrimSpace(filter.Batch)
	filter.Section = strings.ToUpper(strings.TrimSpace(filter.Section))
	if filter.BranchID == "" || filter.Batch == "" || filter.Section == "" {
		return nil, validation("branch_id", "required", "Branch, batch and section are all required.")
	}
	if _, err := uuid.Parse(filter.BranchID); err != nil {
		return nil, validation("branch_id", "invalid_format", "branch_id must be a UUID.")
	}
	// Scoped to the ACTIVE version: a token sealed under a retired key is not
	// the card the populator is about to write, so counting it would report a
	// section as done that is not.
	return s.ledger.SectionStatus(ctx, s.keyring.ActiveVersion(), filter)
}

func (s *Service) ResolveToken(ctx context.Context, input ResolveInput) (ResolvedStudent, error) {
	version := strings.TrimSpace(input.Version)
	token := strings.TrimSpace(input.Token)
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if version == "" || err != nil || len(decoded) != sha256.Size {
		return ResolvedStudent{}, cardNotRecognized()
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != token {
		return ResolvedStudent{}, cardNotRecognized()
	}
	if _, ok := s.keyring.key(version); !ok {
		return ResolvedStudent{}, cardNotRecognized()
	}
	fingerprint := sha256.Sum256([]byte(token))
	student, err := s.ledger.Resolve(ctx, version, fingerprint)
	if errors.Is(err, ErrCardNotRecognized) {
		return ResolvedStudent{}, cardNotRecognized()
	}
	if err != nil {
		return ResolvedStudent{}, err
	}
	expected, err := s.token(version, student.RollNo)
	if err != nil {
		return ResolvedStudent{}, err
	}
	if !hmac.Equal([]byte(expected), []byte(token)) {
		return ResolvedStudent{}, cardNotRecognized()
	}
	return resolved(student), nil
}

func (s *Service) token(version, rollNo string) (string, error) {
	key, ok := s.keyring.key(version)
	if !ok {
		return "", fmt.Errorf("cardtoken: seal version %q has no configured key", version)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(rollNo))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func resolved(student Student) ResolvedStudent {
	return ResolvedStudent{
		StudentID: student.ID, RollNo: student.RollNo, DisplayName: student.DisplayName,
		Branch: student.Branch, Section: student.Section, Batch: student.Batch,
	}
}

func forbidden() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Admin role required."}
}

func validation(field, code, message string) domain.DomainError {
	return domain.DomainError{
		Code: contracts.ErrorCodeVALIDATIONFAILED, Message: "Card token validation failed.",
		Details: []contracts.ValidationErrorDetail{{Field: field, Code: code, Message: message}},
	}
}

func studentNotFound() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeSTUDENTNOTFOUND, Message: "Student not found."}
}

func cardNotRecognized() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeCARDNOTRECOGNIZED, Message: "Card not recognized."}
}

func stringPtrOrNil(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}
