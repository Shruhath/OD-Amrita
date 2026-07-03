package auth

import (
	"strings"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

const domainRejectedMessage = "Only @amrita.edu accounts can access this system."

// EnforceDomain accepts the configured domain family: bare host or any
// subdomain at a label boundary. auth.* audit events are reserved for Story 1.7.
func EnforceDomain(email, allowedDomain string) error {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return domainRejected()
	}
	host := strings.ToLower(email[at+1:])
	allowed := strings.ToLower(strings.TrimSpace(allowedDomain))
	if host == allowed || strings.HasSuffix(host, "."+allowed) {
		return nil
	}
	return domainRejected()
}

func domainRejected() domain.DomainError {
	return domain.DomainError{Code: contracts.ErrorCodeAUTHDOMAINREJECTED, Message: domainRejectedMessage}
}
