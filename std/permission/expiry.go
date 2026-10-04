package permission

import (
	"time"

	corepermission "github.com/victorzhuk/gohan/core/permission"
)

// Waker schedules one expiry callback for a pending approval. The runtime
// supplies the implementation; tests inject a recording fake.
type Waker interface {
	Schedule(token string, at time.Time)
}

// ExpiryVerdict says what a fired token does to its pending call.
type ExpiryVerdict int

const (
	Reject ExpiryVerdict = iota
	Escalate
)

// ExpiryTarget is the re-suspension an escalation produces: the run asks
// again with the next escalation scope eligible.
type ExpiryTarget struct {
	Token    string
	Eligible []string
	At       time.Time
}

// ExpiryQueue tracks pending approvals and resolves each token's verdict
// when its time runs out, per the request's OnExpiry action.
type ExpiryQueue struct {
	waker   Waker
	pending map[string]*pendingExpiry
}

type pendingExpiry struct {
	req    corepermission.ApprovalRequest
	policy corepermission.ApprovalPolicy
	index  int
}

// NewExpiryQueue builds the queue against the injected waker.
func NewExpiryQueue(w Waker) *ExpiryQueue {
	return &ExpiryQueue{waker: w, pending: map[string]*pendingExpiry{}}
}

// Track arms one pending token; the waker hears the request's own expiry
// time.
func (q *ExpiryQueue) Track(token string, req corepermission.ApprovalRequest, policy corepermission.ApprovalPolicy) {
	q.pending[token] = &pendingExpiry{req: req, policy: policy}
	q.waker.Schedule(token, req.ExpiresAt)
}

// Expire resolves one token the waker fired. RejectOnExpiry rejects the
// pending call; EscalateOnExpiry re-suspends with the next escalation scope
// eligible and arms the waker for the same deadline again. An exhausted
// escalation list rejects. Unknown tokens report false.
func (q *ExpiryQueue) Expire(token string) (ExpiryVerdict, ExpiryTarget, bool) {
	p, ok := q.pending[token]
	if !ok {
		return Reject, ExpiryTarget{}, false
	}
	if p.req.OnExpiry == corepermission.EscalateOnExpiry && p.index < len(p.policy.Escalation) {
		scope := p.policy.Escalation[p.index]
		p.index++
		q.waker.Schedule(token, p.req.ExpiresAt)
		return Escalate, ExpiryTarget{Token: token, Eligible: []string{scope}, At: p.req.ExpiresAt}, true
	}
	delete(q.pending, token)
	return Reject, ExpiryTarget{}, true
}
