package gohan

import (
	"context"
	"fmt"
	"slices"

	"github.com/victorzhuk/gohan/core/types"
)

// Steer reaches only the root run's history, so the caller must first pass
// the session log's ownership check. Another tenant is refused with
// ErrSessionForbidden before the mailbox is touched; a child or fork
// session with no live run refuses with ErrRunNotActive at the lookup.
func (c *conversation) checkSteerOwner(ctx context.Context, sessionID string) error {
	_, err := c.load(ctx, sessionID)
	return err
}

// SessionOwnerReader is the optional per-session owner read on a session
// log. The takeover check compares tenants against it without implying
// read access to the session's history.
type SessionOwnerReader interface {
	Owner(ctx context.Context, sessionID string) (types.SessionOwner, error)
}

// TakeOver moves control to a human, which the permission capability
// reserves to a principal of the owner's tenant carrying the control
// scope; session:write alone does not qualify.
func (c *conversation) checkControlOperator(ctx context.Context, sessionID string, operator types.Principal) error {
	tenant := ""
	if r, ok := c.log.(SessionOwnerReader); ok {
		owner, err := r.Owner(ctx, sessionID)
		if err != nil {
			return err
		}
		tenant = owner.Tenant
	} else {
		h, err := c.load(ctx, sessionID)
		if err != nil {
			return err
		}
		tenant = h.Owner.Tenant
	}
	if operator.Tenant != tenant {
		return fmt.Errorf("operator tenant %s: %w", operator.Tenant, types.ErrSessionForbidden)
	}
	if !slices.Contains(operator.Scopes, types.ScopeSessionControl) {
		return fmt.Errorf("operator %s: %w", operator.Subject, types.ErrSessionForbidden)
	}
	return nil
}
