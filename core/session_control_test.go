package gohan

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// controlRuns records the signals Steer posts against the root run.
type controlRuns struct {
	steerRuns

	mu      sync.Mutex
	signals []stores.Signal
	runs    []string
}

func (r *controlRuns) Signal(_ context.Context, runID string, s stores.Signal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs = append(r.runs, runID)
	r.signals = append(r.signals, s)
	return nil
}

func (r *controlRuns) posted() ([]string, []stores.Signal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.runs...), append([]stores.Signal(nil), r.signals...)
}

func controlSetup(t *testing.T) (*conversation, *stores.MemorySessionLog, *controlRuns) {
	t.Helper()
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	stack, err := Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	stack.stores = stores.Stores{SessionLog: log}
	runs := &controlRuns{}
	conv, err := NewConversation(stack, "chat", &takeoverRT{},
		WithConversationRuns(runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	c, ok := conv.(*conversation)
	if !ok {
		t.Fatalf("NewConversation returned %T, want *conversation", conv)
	}
	return c, log, runs
}

func controlAppend(t *testing.T, log *stores.MemorySessionLog, sess string) {
	t.Helper()
	if _, err := log.Append(principalCtx(context.Background()), sess, 0, userMsg("hello")); err != nil {
		t.Fatalf("append %s: %v", sess, err)
	}
}

func operatorCtx(ctx context.Context, p types.Principal) context.Context {
	return WithPrincipal(ctx, p)
}

func TestSessionControl(t *testing.T) {
	t.Run("identity.steer-root-only", func(t *testing.T) {
		c, log, runs := controlSetup(t)
		controlAppend(t, log, "sess-root")
		c.track("sess-root", "run-1")

		if err := c.Steer(principalCtx(context.Background()), "sess-root", userMsg("steer")); err != nil {
			t.Fatalf("Steer: %v", err)
		}
		ids, signals := runs.posted()
		if len(ids) != 1 || ids[0] != "run-1" {
			t.Fatalf("signals on %v, want the root run run-1", ids)
		}
		if len(signals) != 1 || signals[0].Kind != stores.SignalSteer {
			t.Fatalf("signal kind %v, want steer", signals)
		}

		// A child session of the same owner has no live run of its own:
		// only the root run's history receives steers.
		controlAppend(t, log, "sess-child")
		if err := log.Link(principalCtx(context.Background()), "sess-root", "sess-child", stores.SessionChild); err != nil {
			t.Fatalf("link: %v", err)
		}
		err := c.Steer(principalCtx(context.Background()), "sess-child", userMsg("steer"))
		if !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("child steer err = %v, want ErrRunNotActive", err)
		}

		other := operatorCtx(context.Background(), types.Principal{Tenant: "t-b", Subject: "u9"})
		if err := c.Steer(other, "sess-root", userMsg("steer")); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("cross-tenant steer err = %v, want ErrSessionForbidden", err)
		}
	})

	t.Run("non-root-subject-steer-refused", func(t *testing.T) {
		c, log, runs := controlSetup(t)
		controlAppend(t, log, "sess-root")
		c.track("sess-root", "run-1")

		peer := operatorCtx(context.Background(), types.Principal{Tenant: "t-a", Subject: "u2"})
		err := c.Steer(peer, "sess-root", userMsg("steer"))
		if !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("same-tenant other-subject steer err = %v, want ErrSessionForbidden", err)
		}
		if ids, _ := runs.posted(); len(ids) != 0 {
			t.Fatalf("refused steer signalled %v", ids)
		}
	})

	t.Run("permission.takeover-requires-scope", func(t *testing.T) {
		c, log, runs := controlSetup(t)
		controlAppend(t, log, "sess-root")
		c.track("sess-root", "run-1")

		writer := types.Principal{
			Tenant: "t-a", Subject: "op-1", Scopes: []string{types.ScopeSessionWrite},
		}
		err := c.TakeOver(operatorCtx(context.Background(), writer), "sess-root", "", writer)
		if !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("session:write takeover err = %v, want ErrSessionForbidden", err)
		}
		if ctrl, _ := log.Control(principalCtx(context.Background()), "sess-root"); ctrl != stores.ControlAgent {
			t.Fatal("refused takeover wrote control state")
		}
		if ids, _ := runs.posted(); len(ids) != 0 {
			t.Fatalf("refused takeover signalled %v", ids)
		}

		controller := types.Principal{
			Tenant: "t-a", Subject: "op-1", Scopes: []string{types.ScopeSessionControl},
		}
		if err := c.TakeOver(operatorCtx(context.Background(), controller), "sess-root", "", controller); err != nil {
			t.Fatalf("control-scope takeover: %v", err)
		}
		if ctrl, _ := log.Control(principalCtx(context.Background()), "sess-root"); ctrl != stores.ControlHuman {
			t.Fatalf("control = %v, want ControlHuman", ctrl)
		}
	})

	t.Run("stores.control-in-session-index", func(t *testing.T) {
		tick := time.Unix(1700000000, 0)
		log := stores.NewMemorySessionLog(
			stores.WithSessionPrincipals(types.PrincipalFrom),
			stores.WithMemorySessionClock(func() time.Time { tick = tick.Add(time.Minute); return tick }),
		)
		for _, sess := range []string{"sess-1", "sess-2", "sess-3"} {
			controlAppend(t, log, sess)
		}
		ctx := principalCtx(context.Background())
		if err := log.SetControl(ctx, "sess-1", stores.ControlHandoffRequested); err != nil {
			t.Fatalf("set control: %v", err)
		}
		if err := log.SetControl(ctx, "sess-2", stores.ControlHuman); err != nil {
			t.Fatalf("set control: %v", err)
		}
		if err := log.SetControl(ctx, "sess-3", stores.ControlHandoffRequested); err != nil {
			t.Fatalf("set control: %v", err)
		}

		stack, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		stack.stores = stores.Stores{SessionLog: log}

		requested := stores.ControlHandoffRequested
		metas, _, err := stack.Sessions(ctx, stores.SessionQuery{Control: &requested})
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		if len(metas) != 2 || metas[0].ID != "sess-3" || metas[1].ID != "sess-1" {
			t.Fatalf("handoff queue = %v, want [sess-3 sess-1] by last activity", ids(metas))
		}
	})

	t.Run("index-returns-only-requested-control", func(t *testing.T) {
		tick := time.Unix(1700000000, 0)
		log := stores.NewMemorySessionLog(
			stores.WithSessionPrincipals(types.PrincipalFrom),
			stores.WithMemorySessionClock(func() time.Time { tick = tick.Add(time.Minute); return tick }),
		)
		for _, sess := range []string{"sess-1", "sess-2", "sess-3"} {
			controlAppend(t, log, sess)
		}
		ctx := principalCtx(context.Background())
		_ = log.SetControl(ctx, "sess-2", stores.ControlHuman)
		stack, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		stack.stores = stores.Stores{SessionLog: log}

		human := stores.ControlHuman
		metas, _, err := stack.Sessions(ctx, stores.SessionQuery{Control: &human})
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		if len(metas) != 1 || metas[0].ID != "sess-2" {
			t.Fatalf("human queue = %v, want [sess-2]", ids(metas))
		}
		unfiltered, _, err := stack.Sessions(ctx, stores.SessionQuery{})
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		if len(unfiltered) != 3 {
			t.Fatalf("default listing = %v, want all three", ids(unfiltered))
		}
	})
}

func ids(metas []stores.SessionMeta) []string {
	out := make([]string, len(metas))
	for i, m := range metas {
		out[i] = m.ID
	}
	return out
}
