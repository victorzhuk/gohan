package permission

import (
	"sync"

	"github.com/victorzhuk/gohan/core/types"
)

// PendingLimiter enforces ApprovalPolicy.MaxPending per subject and per
// tenant, so one principal or one tenant cannot flood the approval queue.
type PendingLimiter struct {
	mu     sync.Mutex
	max    int
	counts map[string]int
}

// NewPendingLimiter caps each (tenant, subject) pair at max pending
// approvals; zero or negative falls back to DefaultMaxPending.
func NewPendingLimiter(max int) *PendingLimiter {
	return &PendingLimiter{max: max, counts: map[string]int{}}
}

// Add counts one pending approval. Beyond the cap it fails with
// *types.LimitExceededError{Limit: "pending_approvals"} and counts nothing.
func (l *PendingLimiter) Add(subject, tenant string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := tenant + "\x00" + subject
	max := l.max
	if max < 1 {
		max = DefaultMaxPending
	}
	if l.counts[key] >= max {
		return &types.LimitExceededError{Limit: "pending_approvals", Value: float64(max)}
	}
	l.counts[key]++
	return nil
}

// Release drops one pending approval after it resolves.
func (l *PendingLimiter) Release(subject, tenant string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := tenant + "\x00" + subject
	if l.counts[key] > 0 {
		l.counts[key]--
	}
}
