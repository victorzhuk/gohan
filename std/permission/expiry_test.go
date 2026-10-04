package permission

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	corepermission "github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
)

type fakeWaker struct {
	schedules []schedule
}

type schedule struct {
	token string
	at    time.Time
}

func (w *fakeWaker) Schedule(token string, at time.Time) {
	w.schedules = append(w.schedules, schedule{token, at})
}

func expiryRequest(action corepermission.ExpiryAction) corepermission.ApprovalRequest {
	return corepermission.ApprovalRequest{
		Tool:      types.ToolSpec{Name: "send_refund", Risk: types.RiskMedium},
		Risk:      types.RiskMedium,
		ExpiresAt: time.Now().Add(time.Hour),
		OnExpiry:  action,
	}
}

func TestApprovalQueue(t *testing.T) {
	t.Run("permission.expiry-default", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			w := &fakeWaker{}
			q := NewExpiryQueue(w)
			req := expiryRequest(corepermission.RejectOnExpiry)
			q.Track("tok1", req, corepermission.ApprovalPolicy{})
			if len(w.schedules) != 1 || !w.schedules[0].at.Equal(req.ExpiresAt) {
				t.Fatalf("waker schedule: %+v, want token tok1 at %v", w.schedules, req.ExpiresAt)
			}
			time.Sleep(time.Hour)
			verdict, _, ok := q.Expire("tok1")
			if !ok || verdict != Reject {
				t.Fatalf("expired RejectOnExpiry token: verdict=%v ok=%v, want Reject", verdict, ok)
			}
		})
	})

	t.Run("permission.escalation-targets", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			w := &fakeWaker{}
			q := NewExpiryQueue(w)
			req := expiryRequest(corepermission.EscalateOnExpiry)
			policy := corepermission.ApprovalPolicy{Escalation: []string{"approve:refunds", "approve:finance-lead"}}
			q.Track("tok1", req, policy)

			verdict, target, ok := q.Expire("tok1")
			if !ok || verdict != Escalate {
				t.Fatalf("escalating token: verdict=%v ok=%v, want Escalate", verdict, ok)
			}
			if len(target.Eligible) != 1 || target.Eligible[0] != "approve:refunds" {
				t.Fatalf("first target: %+v, want [approve:refunds]", target.Eligible)
			}
			if n := len(w.schedules); n != 2 {
				t.Fatalf("waker heard %d schedules, want 2", n)
			}

			verdict, target, _ = q.Expire("tok1")
			if verdict != Escalate || target.Eligible[0] != "approve:finance-lead" {
				t.Fatalf("second target: verdict=%v eligible=%v, want approve:finance-lead", verdict, target.Eligible)
			}

			verdict, _, _ = q.Expire("tok1")
			if verdict != Reject {
				t.Fatalf("exhausted escalation list: verdict=%v, want Reject", verdict)
			}
		})
	})

	t.Run("permission.queue-flood", func(t *testing.T) {
		l := NewPendingLimiter(2)
		if err := l.Add("op", "acme"); err != nil {
			t.Fatalf("first pending: %v", err)
		}
		if err := l.Add("op", "acme"); err != nil {
			t.Fatalf("second pending: %v", err)
		}
		err := l.Add("op", "acme")
		var limit *types.LimitExceededError
		if !errors.As(err, &limit) || limit.Limit != "pending_approvals" {
			t.Fatalf("further ask: got %v, want *LimitExceededError{pending_approvals}", err)
		}
		if err := l.Add("peer", "acme"); err != nil {
			t.Fatalf("other subject capped too: %v", err)
		}
		if err := l.Add("op", "globex"); err != nil {
			t.Fatalf("other tenant capped too: %v", err)
		}
		l.Release("op", "acme")
		if err := l.Add("op", "acme"); err != nil {
			t.Fatalf("released slot not reusable: %v", err)
		}
	})
}
