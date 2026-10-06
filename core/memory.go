package gohan

import (
	"context"
	"fmt"
	"slices"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// memoryKey addresses one subject's memory from the checked owner, so no
// caller can name another tenant or subject.
func memoryKey(owner types.SessionOwner) stores.NotesKey {
	return stores.NotesKey{
		Scope:   stores.ScopeSubject,
		Tenant:  owner.Tenant,
		Subject: owner.Subject,
	}
}

// checkMemoryOwner is the Inspect ownership check against an explicit
// owner: the caller must carry the owner's tenant and either its subject
// or a session:read scope.
func (s *Stack) checkMemoryOwner(ctx context.Context, owner types.SessionOwner) (types.Principal, error) {
	if err := requirePrincipal(ctx, s.allowAnonymous); err != nil {
		return types.Principal{}, err
	}
	p, _ := types.PrincipalFrom(ctx)
	if p.Tenant != owner.Tenant {
		return p, fmt.Errorf("memory owner tenant %s: %w", p.Tenant, types.ErrSessionForbidden)
	}
	if p.Subject != owner.Subject && !slices.Contains(p.Scopes, types.ScopeSessionRead) {
		return p, fmt.Errorf("memory owner subject %s: %w", p.Subject, types.ErrSessionForbidden)
	}
	return p, nil
}

func (s *Stack) memoryStore() (stores.MemoryStore, error) {
	mem, ok := s.stores.Notes.(stores.MemoryStore)
	if !ok {
		return nil, types.ErrMemoryStoreRequired
	}
	return mem, nil
}

// Memory lists the owner's subject-memory entries. The caller must own the
// owner like Inspect requires, or the call is refused with
// ErrSessionForbidden.
func (s *Stack) Memory(ctx context.Context, owner types.SessionOwner) ([]stores.MemoryEntry, error) {
	if _, err := s.checkMemoryOwner(ctx, owner); err != nil {
		return nil, err
	}
	mem, err := s.memoryStore()
	if err != nil {
		return nil, err
	}
	entries, err := mem.Entries(ctx, memoryKey(owner))
	if err != nil {
		return nil, fmt.Errorf("memory %s/%s: %w", owner.Tenant, owner.Subject, err)
	}
	return entries, nil
}

// ForgetMemory drops the named subject-memory entries. The ownership rule
// and the key are Memory's.
func (s *Stack) ForgetMemory(ctx context.Context, owner types.SessionOwner, ids ...string) error {
	if _, err := s.checkMemoryOwner(ctx, owner); err != nil {
		return err
	}
	mem, err := s.memoryStore()
	if err != nil {
		return err
	}
	if err := mem.Forget(ctx, memoryKey(owner), ids...); err != nil {
		return fmt.Errorf("forget memory %s/%s: %w", owner.Tenant, owner.Subject, err)
	}
	return nil
}
