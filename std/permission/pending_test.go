package permission

import (
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestPendingLimiter(t *testing.T) {
	l := NewPendingLimiter(1)
	if err := l.Add("op", "acme"); err != nil {
		t.Fatalf("first pending: %v", err)
	}
	err := l.Add("op", "acme")
	var limit *types.LimitExceededError
	if !errors.As(err, &limit) || limit.Limit != "pending_approvals" {
		t.Fatalf("second pending: got %v, want *LimitExceededError{pending_approvals}", err)
	}
	l.Release("op", "acme")
	if err := l.Add("op", "acme"); err != nil {
		t.Fatalf("released slot not reusable: %v", err)
	}
	l2 := NewPendingLimiter(0)
	if err := l2.Add("op", "acme"); err != nil {
		t.Fatalf("zero cap must fall back to the default: %v", err)
	}
}
