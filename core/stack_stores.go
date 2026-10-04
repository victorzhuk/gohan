package gohan

import (
	"context"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Stores returns the store ports the stack runs against. A zero port field
// means the operation that needs it is refused at the accessor's consumer.
func (s *Stack) Stores() stores.Stores { return s.stores }

// Sessions lists sessions through the store's session index, scoped to the
// caller principal's owner.
func (s *Stack) Sessions(ctx context.Context, q stores.SessionQuery) ([]stores.SessionMeta, string, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, "", types.ErrNoPrincipal
	}
	idx, ok := s.stores.SessionLog.(stores.SessionIndex)
	if !ok {
		return nil, "", types.ErrSessionIndexRequired
	}
	return idx.Sessions(ctx, types.SessionOwner{Tenant: p.Tenant, Subject: p.Subject}, q)
}
