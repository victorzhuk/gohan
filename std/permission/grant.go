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

// Grant is the stored permission one approval produced: a call of tool with
// this fingerprint by subject skips the gate until ExpiresAt.
type Grant struct {
	Tool        string
	Fingerprint stores.Fingerprint
	Subject     string
	Approver    string
	ExpiresAt   time.Time
}

// GrantStore keeps session grants. Grants live inside one session only; a
// fork or a new session starts empty.
type GrantStore interface {
	Put(ctx context.Context, sessionID string, g Grant) error
	Find(ctx context.Context, sessionID, tool string, fp stores.Fingerprint, subject string) (Grant, bool, error)
}

// MemoryGrantStore is the in-memory GrantStore.
type MemoryGrantStore struct {
	mu     sync.Mutex
	grants map[string][]Grant
	now    func() time.Time
}

// NewMemoryGrantStore builds the in-memory store; now supplies the clock
// grant expiry is judged by.
func NewMemoryGrantStore(now func() time.Time) *MemoryGrantStore {
	return &MemoryGrantStore{grants: map[string][]Grant{}, now: now}
}

// Put stores one grant.
func (s *MemoryGrantStore) Put(ctx context.Context, sessionID string, g Grant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.grants[sessionID] = append(s.grants[sessionID], g)
	return nil
}

// Find returns the live grant for the call, or false when none matches or
// the only matches have expired.
func (s *MemoryGrantStore) Find(ctx context.Context, sessionID, tool string, fp stores.Fingerprint, subject string) (Grant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, g := range s.grants[sessionID] {
		if g.Tool == tool && g.Fingerprint == fp && g.Subject == subject && g.ExpiresAt.After(now) {
			return g, true, nil
		}
	}
	return Grant{}, false, nil
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
// the approver must pass the eligibility check, the TTL is capped at
// MaxGrantTTL, and the grant is stored before the audit record is returned.
func ApproveGrant(ctx context.Context, store GrantStore, sessionID string, req permission.ApprovalRequest, approver types.Principal, ttl time.Duration) (Grant, GrantAudit, error) {
	if err := permission.CheckEligibility(req, approver); err != nil {
		return Grant{}, GrantAudit{}, err
	}
	if ttl <= 0 || ttl > MaxGrantTTL {
		ttl = MaxGrantTTL
	}
	g := Grant{
		Tool:        req.Tool.Name,
		Fingerprint: req.Fingerprint,
		Subject:     approver.Subject,
		Approver:    approver.Subject,
		ExpiresAt:   req.ExpiresAt.Add(ttl),
	}
	if err := store.Put(ctx, sessionID, g); err != nil {
		return Grant{}, GrantAudit{}, fmt.Errorf("store grant: %w", err)
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
func HasGrant(ctx context.Context, store GrantStore, sessionID string, inv *permission.ToolInvocation, fp stores.Fingerprint, subject string) (bool, error) {
	_, ok, err := store.Find(ctx, sessionID, inv.Spec.Name, fp, subject)
	return ok, err
}
