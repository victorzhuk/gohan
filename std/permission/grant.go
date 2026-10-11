package permission

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// MaxGrantTTL caps the lifetime an ApproveScope grant may request.
const MaxGrantTTL = 24 * time.Hour

// GrantedByScope is the audit event a stored grant produces.
const GrantedByScope = "granted_by_scope"

// ErrGrantRefused reports a grant the policy or the TTL cap rejected.
var ErrGrantRefused = errors.New("gohan: grant refused")

// MemoryGrantStore is the in-memory GrantStore.
type MemoryGrantStore struct {
	mu     sync.Mutex
	grants map[string][]permission.Grant
	now    func() time.Time
}

// NewMemoryGrantStore builds the in-memory store; now supplies the clock
// grant expiry is judged by.
func NewMemoryGrantStore(now func() time.Time) *MemoryGrantStore {
	return &MemoryGrantStore{grants: map[string][]permission.Grant{}, now: now}
}

// Put stores one grant under this store's policy: a caller that asks for a
// lifetime beyond MaxGrantTTL gets the cap, so no grant outlives the policy
// whatever the caller computed. An expiry already in the past is stored as
// given and never matches a lookup.
func (s *MemoryGrantStore) Put(ctx context.Context, sessionID string, g permission.Grant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit := s.now().Add(MaxGrantTTL); g.ExpiresAt.After(limit) {
		g.ExpiresAt = limit
	}
	s.grants[sessionID] = append(s.grants[sessionID], g)
	return nil
}

// Find returns the live grant for the call, or false when none matches or
// the only matches have expired.
func (s *MemoryGrantStore) Find(ctx context.Context, sessionID, tool string, fp stores.Fingerprint, subject string) (permission.Grant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, g := range s.grants[sessionID] {
		if g.Tool == tool && g.Fingerprint == fp && g.Subject == subject && g.ExpiresAt.After(now) {
			return g, true, nil
		}
	}
	return permission.Grant{}, false, nil
}

// GrantAudit is the audit record a stored grant produces.
type GrantAudit struct {
	Event     string
	Tool      string
	Subject   string
	Approver  string
	Scope     string
	ExpiresAt time.Time
}

// ApproveGrant grants the suspended call's identity under the tier's policy:
// the approver must pass the eligibility check, the expiry derives from
// the approval instant grantedAt, never from req.ExpiresAt. The TTL is
// capped at MaxGrantTTL, and the grant is stored before the audit record
// is returned.
func ApproveGrant(ctx context.Context, store permission.GrantStore, sessionID string, req permission.ApprovalRequest, approver types.Principal, ttl time.Duration, grantedAt time.Time) (permission.Grant, GrantAudit, error) {
	if err := permission.CheckEligibility(req, approver); err != nil {
		return permission.Grant{}, GrantAudit{}, err
	}
	if ttl <= 0 || ttl > MaxGrantTTL {
		ttl = MaxGrantTTL
	}
	g := permission.Grant{
		Tool:        req.Tool.Name,
		Fingerprint: req.Fingerprint,
		Subject:     approver.Subject,
		Approver:    approver.Subject,
		ExpiresAt:   grantedAt.Add(ttl),
	}
	if err := store.Put(ctx, sessionID, g); err != nil {
		return permission.Grant{}, GrantAudit{}, fmt.Errorf("store grant: %w", err)
	}
	return g, GrantAudit{
		Event:     GrantedByScope,
		Tool:      g.Tool,
		Subject:   g.Subject,
		Approver:  g.Approver,
		Scope:     "approve:" + g.Tool,
		ExpiresAt: g.ExpiresAt,
	}, nil
}

// HasGrant reports whether a live grant covers the invocation's identity in
// the session: the same tool, the same fingerprint and the same subject.
func HasGrant(ctx context.Context, store permission.GrantStore, sessionID string, inv *permission.ToolInvocation, fp stores.Fingerprint, subject string) (bool, error) {
	_, ok, err := store.Find(ctx, sessionID, inv.Spec.Name, fp, subject)
	return ok, err
}
