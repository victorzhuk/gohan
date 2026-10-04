package stores

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/victorzhuk/gohan/core/types"
)

// Feedback is one recorded feedback entry. Version is store-assigned and
// starts at 1; a later write with the same name replaces the entry and
// bumps it.
type Feedback struct {
	Target     types.FeedbackTarget
	Name       string
	Value      any
	Comment    string
	Correction []types.Block
	Source     types.FeedbackSource
	Version    int64
}

// FeedbackStore records per-session feedback. One entry exists per name per
// session; a second write with the same name overwrites it and bumps the
// version. Feedback is quality telemetry: it never reaches the model
// context, so nothing here feeds back into History.
type FeedbackStore interface {
	PutFeedback(ctx context.Context, f Feedback) (int64, error)
	Feedback(ctx context.Context, sessionID string) ([]Feedback, error)
}

// MemoryFeedbackOption configures NewMemoryFeedback.
type MemoryFeedbackOption func(*MemoryFeedback)

// WithMemoryFeedbackPrincipals injects the principal extractor used for the
// ownership checks.
func WithMemoryFeedbackPrincipals(src PrincipalSource) MemoryFeedbackOption {
	return func(s *MemoryFeedback) { s.principals = src }
}

// WithFeedbackSessionOwner injects the lookup that resolves a session's
// owner. The session log owns the mapping; the feedback store consumes it.
func WithFeedbackSessionOwner(find func(ctx context.Context, sessionID string) (types.SessionOwner, error)) MemoryFeedbackOption {
	return func(s *MemoryFeedback) { s.owners = find }
}

// MemoryFeedback is the in-memory FeedbackStore reference implementation.
type MemoryFeedback struct {
	mu         sync.Mutex
	principals PrincipalSource
	owners     func(ctx context.Context, sessionID string) (types.SessionOwner, error)
	rows       map[string]map[string]Feedback
}

func NewMemoryFeedback(opts ...MemoryFeedbackOption) *MemoryFeedback {
	s := &MemoryFeedback{}
	for _, o := range opts {
		o(s)
	}
	return s
}

// PutFeedback validates the caller against the session owner, then records f
// under its name. An existing entry with the same name is replaced and the
// returned version is the entry's new version, starting at 1. Feedback and
// Correction are copied: nothing the caller passed stays aliased.
func (s *MemoryFeedback) PutFeedback(ctx context.Context, f Feedback) (int64, error) {
	if err := s.checkAccess(ctx, f.Target.SessionID, scopeSessionWrite); err != nil {
		return 0, err
	}

	f.Correction = copyBlocks(f.Correction)
	s.mu.Lock()
	defer s.mu.Unlock()
	byName := s.rows[f.Target.SessionID]
	if byName == nil {
		byName = make(map[string]Feedback)
		if s.rows == nil {
			s.rows = make(map[string]map[string]Feedback)
		}
		s.rows[f.Target.SessionID] = byName
	}
	f.Version = byName[f.Name].Version + 1
	byName[f.Name] = f
	return f.Version, nil
}

// Feedback lists the session's entries in name order. The entries are
// copies; mutating them does not touch the store.
func (s *MemoryFeedback) Feedback(ctx context.Context, sessionID string) ([]Feedback, error) {
	if err := s.checkAccess(ctx, sessionID, scopeSessionRead); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	byName := s.rows[sessionID]
	out := make([]Feedback, 0, len(byName))
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		f := byName[name]
		f.Correction = copyBlocks(f.Correction)
		out = append(out, f)
	}
	return out, nil
}

func (s *MemoryFeedback) checkAccess(ctx context.Context, sessionID, scope string) error {
	if s.principals == nil {
		return types.ErrNoPrincipal
	}
	p, ok := s.principals(ctx)
	if !ok {
		return types.ErrNoPrincipal
	}
	if s.owners == nil {
		return fmt.Errorf("feedback for session %s: owner lookup is not wired", sessionID)
	}
	owner, err := s.owners(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("session %s: %w", sessionID, err)
	}
	if p.Tenant != owner.Tenant {
		return fmt.Errorf("tenant %s: %w", p.Tenant, types.ErrSessionForbidden)
	}
	if p.Subject != owner.Subject && !slices.Contains(p.Scopes, scope) {
		return fmt.Errorf("subject %s: %w", p.Subject, types.ErrSessionForbidden)
	}
	return nil
}

func copyBlocks(bs []types.Block) []types.Block {
	if bs == nil {
		return nil
	}
	return slices.Clone(bs)
}
