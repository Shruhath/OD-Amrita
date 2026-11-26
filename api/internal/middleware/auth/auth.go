package auth

import (
	"context"
	"log"
	"strings"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	svcauth "github.com/Shruhath/OD-Amrita/api/internal/service/auth"
)

const identityKey = "auth.identity"

// domainRejectReason is the sanitized reason recorded on an auth.domain_rejected
// audit row. It is a fixed classification, never the raw email or provider detail.
const domainRejectReason = "email domain not permitted"

// Auditor writes the auth.domain_rejected audit event. The middleware
// keeps only this narrow rejection view of *service/auth.EventAuditor.
type Auditor interface {
	DomainRejected(ctx context.Context, email, device, reason string) error
}

// Middleware verifies the bearer token, enforces the Amrita domain family, and
// stashes the verified identity for handlers. A domain rejection is aborted here
// (never reaching a handler), so it is also the one place auth.domain_rejected can
// be audited — after a VALID token, so id.Email is a trusted account.
func Middleware(verifier svcauth.TokenVerifier, allowedDomain string, auditor Auditor) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			// No verified identity and no registered event — an invalid/missing
			// bearer is a normal 401 (the 1.6 silent-refresh happy path), NOT audited.
			_ = c.Error(domain.DomainError{Code: contracts.ErrorCodeAUTHTOKENINVALID, Message: "Authentication token is invalid."})
			c.Abort()
			return
		}
		id, err := verifier.Verify(c.Request.Context(), token)
		if err != nil {
			// Same: verify failure has no verified identity — 401, not audited.
			_ = c.Error(err)
			c.Abort()
			return
		}
		if derr := svcauth.EnforceDomain(id.Email, allowedDomain); derr != nil {
			// The token verified, so id.Email is a trusted account (FR48 wants it on
			// a rejection). Audit the rejection through the immutable ledger, then
			// still reject 403. device = the request User-Agent (the client IP is
			// not recorded yet).
			device := strings.TrimSpace(c.Request.UserAgent())
			if auditErr := auditor.DomainRejected(c.Request.Context(), id.Email, device, domainRejectReason); auditErr != nil {
				// Best-effort audit (D3, ratified 2026-07-08): a ledger outage must
				// NOT turn a domain rejection into a 500 — the caller is still
				// rejected 403. Log loudly so the missed audit row is alertable
				// (anomaly surfacing); the rejection itself is unaffected.
				// %q escapes the email (a token claim we do not control), so a
				// CR/LF/control-char address cannot forge log lines and poison the
				// anomaly signal this line exists to raise.
				log.Printf("auth: domain-rejection audit write failed for %q (rejection still enforced; audit row missing): %v", id.Email, auditErr)
			}
			_ = c.Error(derr)
			c.Abort()
			return
		}
		SetIdentity(c, id)
		c.Next()
	}
}

func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}

func SetIdentity(c *gin.Context, id svcauth.VerifiedIdentity) {
	c.Set(identityKey, id)
}

func IdentityFromContext(c *gin.Context) (svcauth.VerifiedIdentity, bool) {
	raw, ok := c.Get(identityKey)
	if !ok {
		return svcauth.VerifiedIdentity{}, false
	}
	id, ok := raw.(svcauth.VerifiedIdentity)
	return id, ok
}
