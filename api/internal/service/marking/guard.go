package marking

import (
	"context"
	"errors"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
	odrecord "github.com/Shruhath/OD-Amrita/api/internal/service/od_record"
)

// MarkGuard adapts the policy to the mark service's ScopeGuard.
//
// The import points this way — the policy package knowing the mark service's
// actor type — on purpose. The alternative is the mark service importing the
// policy, which would let a future change reach into the policy from inside the
// write path. Keeping the dependency here means service/od_record holds no
// opinion at all about who may mark; it just calls the guard it was handed.
//
// It also maps the policy's sentinel errors onto the closed domain registry, so
// every refusal leaves this seam as a typed error the centralized middleware can
// render. A raw sentinel escaping to the middleware would surface as a 500.
type MarkGuard struct {
	policy *Policy
}

func NewMarkGuard(policy *Policy) *MarkGuard {
	return &MarkGuard{policy: policy}
}

func (g *MarkGuard) AssertCanMark(ctx context.Context, actor odrecord.Actor, eventID, categoryID string) error {
	err := g.policy.AssertCanMark(ctx, Actor{
		UserID: actor.UserID,
		Email:  actor.Email,
		Role:   actor.Role,
	}, eventID, categoryID)
	return AsDomainError(err)
}

// AsDomainError maps a policy refusal onto the existing error registry. Story
// 0.6 deliberately introduces no new error code: every refusal this seam can
// produce already has one.
func AsDomainError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotCoordinator):
		return domain.DomainError{
			Code:    contracts.ErrorCodeRBACFORBIDDEN,
			Message: "Only a coordinator or admin may mark attendance from this surface.",
		}
	case errors.Is(err, ErrEventNotFound):
		return domain.DomainError{Code: contracts.ErrorCodeEVENTNOTFOUND, Message: "Event not found."}
	case errors.Is(err, ErrEventArchived):
		return domain.DomainError{Code: contracts.ErrorCodeEVENTARCHIVED, Message: "Event is archived."}
	case errors.Is(err, ErrEventNotAssigned):
		return domain.DomainError{Code: contracts.ErrorCodeRBACFORBIDDEN, Message: "Actor is not assigned to this event."}
	case errors.Is(err, ErrCategoryNotFound):
		return domain.DomainError{Code: contracts.ErrorCodeCATEGORYNOTFOUND, Message: "Category not found."}
	default:
		return err
	}
}
