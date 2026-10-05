package gohan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// cancelRuns counts posted signals so the test can tell an authorized
// cancel from a refused one.
type cancelRuns struct {
	*stores.MemoryRuns
	signals int
}

func (f *cancelRuns) Signal(ctx context.Context, runID string, sig stores.Signal) error {
	f.signals++
	return f.MemoryRuns.Signal(ctx, runID, sig)
}

func TestConversationCancelAuthorization(t *testing.T) {
	owner := types.Principal{Tenant: "t-a", Subject: "u1"}
	foreign := types.Principal{Tenant: "t-b", Subject: "u2"}
	sid := "sess-cancel"

	setup := func(t *testing.T) (*conversation, *cancelRuns, *time.Time) {
		t.Helper()
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		if _, err := log.Append(types.WithPrincipal(context.Background(), owner), sid, 0, userMsg("hi")); err != nil {
			t.Fatalf("seed history: %v", err)
		}
		clock := time.Now()
		runs := &cancelRuns{MemoryRuns: stores.NewMemoryRuns(
			stores.WithMemoryRunClock(func() time.Time { return clock }),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		)}
		stack, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		stack.stores = stores.Stores{SessionLog: log}
		conv, err := NewConversation(stack, "chat", &convRT{},
			WithConversationRuns(runs),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		)
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}
		lease, err := runs.Start(types.WithPrincipal(context.Background(), owner), stores.Run{
			SessionID: sid,
			RunID:     "run-c",
		}, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		_ = lease
		c := conv.(*conversation)
		c.track(sid, "run-c")
		return c, runs, &clock
	}

	t.Run("identity.sessions-owner-checked", func(t *testing.T) {
		c, runs, _ := setup(t)
		err := c.Cancel(types.WithPrincipal(context.Background(), foreign), sid)
		if !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("foreign cancel: got %v, want ErrSessionForbidden", err)
		}
		if runs.signals != 0 {
			t.Fatalf("foreign cancel posted %d signals, want 0", runs.signals)
		}
	})

	t.Run("owner cancel posts the signal", func(t *testing.T) {
		c, runs, clock := setup(t)
		// The lease is already expired on the store clock, so Cancel does
		// not wait out the TTL after the signal.
		*clock = clock.Add(2 * stores.LeaseTTL)
		if err := c.Cancel(types.WithPrincipal(context.Background(), owner), sid); err != nil {
			t.Fatalf("owner cancel: %v", err)
		}
		if runs.signals != 1 {
			t.Fatalf("owner cancel posted %d signals, want 1", runs.signals)
		}
	})
}
