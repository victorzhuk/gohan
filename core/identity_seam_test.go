package gohan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestAllowAnonymous(t *testing.T) {
	newConv := func(t *testing.T, log stores.SessionLog, runs *countingRuns, events *countingEvents, opts ...Option) Conversation {
		t.Helper()
		stack, err := Build(opts...)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		stack.stores = stores.Stores{SessionLog: log}
		conv, err := NewConversation(stack, "chat", &convRT{},
			WithConversationRuns(runs), WithConversationEventLog(events))
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}
		return conv
	}

	t.Run("identity.no-principal", func(t *testing.T) {
		conv := newConv(t, stores.NewMemorySessionLog(), &countingRuns{}, &countingEvents{})
		var got []error
		for _, err := range conv.Send(context.Background(), "s1", userMsg("hi")) {
			got = append(got, err)
		}
		if len(got) != 1 || !errors.Is(got[0], types.ErrNoPrincipal) {
			t.Fatalf("send without principal: got %v, want single ErrNoPrincipal", got)
		}
		var cerr error
		for _, err := range conv.Continue(context.Background(), "s1") {
			cerr = err
		}
		if !errors.Is(cerr, types.ErrNoPrincipal) {
			t.Fatalf("continue without principal: got %v, want ErrNoPrincipal", cerr)
		}
		if err := conv.Steer(context.Background(), "s1", userMsg("s")); !errors.Is(err, types.ErrNoPrincipal) {
			t.Fatalf("steer without principal: got %v, want ErrNoPrincipal", err)
		}
		if err := conv.Cancel(context.Background(), "s1"); !errors.Is(err, types.ErrNoPrincipal) {
			t.Fatalf("cancel without principal: got %v, want ErrNoPrincipal", err)
		}
	})

	t.Run("identity.no-principal-at-seam", func(t *testing.T) {
		log := &countingLog{SessionLog: stores.NewMemorySessionLog()}
		runs := &countingRuns{}
		events := &countingEvents{EventLog: stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))}
		conv := newConv(t, log, runs, events)
		for _, err := range conv.Send(context.Background(), "s1", userMsg("hi")) {
			if !errors.Is(err, types.ErrNoPrincipal) {
				t.Fatalf("send error: got %v, want ErrNoPrincipal", err)
			}
		}
		if log.loads != 0 || log.appends != 0 {
			t.Fatalf("session log touched: loads=%d appends=%d", log.loads, log.appends)
		}
		if runs.starts != 0 {
			t.Fatalf("runs store touched: starts=%d", runs.starts)
		}
		if events.appends != 0 {
			t.Fatalf("event log touched: appends=%d", events.appends)
		}
		for _, err := range events.Read(context.Background(), "run-00000001", 0) {
			if err != nil {
				t.Fatalf("event read: %v", err)
			}
			t.Fatalf("unexpected event: %v", err)
		}
	})

	t.Run("anonymous flow func succeeds", func(t *testing.T) {
		f := FlowFunc[string, string]("anon", func(_ context.Context, in string) (string, error) {
			return "x:" + in, nil
		})
		if _, err := f.Invoke(context.Background(), "in"); !errors.Is(err, types.ErrNoPrincipal) {
			t.Fatalf("invoke without option: got %v, want ErrNoPrincipal", err)
		}
		anon := FlowFunc[string, string]("anon", func(_ context.Context, in string) (string, error) {
			return "x:" + in, nil
		}, AllowAnonymousFlow())
		out, err := anon.Invoke(context.Background(), "in")
		if err != nil || out != "x:in" {
			t.Fatalf("anonymous invoke: got (%q, %v)", out, err)
		}
	})

	t.Run("anonymous unowned session send refused", func(t *testing.T) {
		runs := &countingRuns{Runs: stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now))}
		events := &countingEvents{EventLog: stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))}
		conv := newConv(t, stores.NewMemorySessionLog(), runs, events, AllowAnonymous())
		var got error
		for _, err := range conv.Send(context.Background(), "anon-s1", userMsg("hi")) {
			got = err
		}
		if !errors.Is(got, types.ErrSessionForbidden) {
			t.Fatalf("anonymous send on session face: got %v, want ErrSessionForbidden", got)
		}
		if runs.starts != 0 {
			t.Fatalf("anonymous send acquired a lease: starts=%d", runs.starts)
		}
	})

	t.Run("anonymous owned session send refused", func(t *testing.T) {
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		ctx := WithPrincipal(context.Background(), types.Principal{Tenant: "t-a", Subject: "u1"})
		if _, err := log.Append(ctx, "owned", 0, userMsg("seed")); err != nil {
			t.Fatalf("seed: %v", err)
		}
		runs := &countingRuns{Runs: stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now))}
		events := &countingEvents{EventLog: stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))}
		conv := newConv(t, log, runs, events, AllowAnonymous())
		var got error
		for _, err := range conv.Send(context.Background(), "owned", userMsg("hi")) {
			got = err
		}
		if !errors.Is(got, types.ErrSessionForbidden) {
			t.Fatalf("anonymous send on owned session: got %v, want ErrSessionForbidden", got)
		}
		if runs.starts != 0 {
			t.Fatalf("owned session acquired a lease: starts=%d", runs.starts)
		}
	})
}

type countingLog struct {
	stores.SessionLog
	loads   int
	appends int
}

func (l *countingLog) Load(ctx context.Context, sessionID string) (stores.History, error) {
	l.loads++
	return l.SessionLog.Load(ctx, sessionID)
}

func (l *countingLog) Append(ctx context.Context, sessionID string, v int64, msgs ...types.Message) (int64, error) {
	l.appends++
	return l.SessionLog.Append(ctx, sessionID, v, msgs...)
}

type countingRuns struct {
	stores.Runs
	starts int
}

func (r *countingRuns) Start(ctx context.Context, run stores.Run, ttl time.Duration) (stores.Lease, error) {
	r.starts++
	return r.Runs.Start(ctx, run, ttl)
}

type countingEvents struct {
	stores.EventLog
	appends int
}

func (e *countingEvents) Append(ctx context.Context, runID string, ev stores.Event) error {
	e.appends++
	return e.EventLog.Append(ctx, runID, ev)
}

func TestPrincipalSeam(t *testing.T) {
	t.Run("identity.no-principal", func(t *testing.T) {
		err := requirePrincipal(context.Background(), false)
		if !errors.Is(err, types.ErrNoPrincipal) {
			t.Fatalf("flow without AllowAnonymous invoked without a principal: got %v, want ErrNoPrincipal", err)
		}

		if err := requirePrincipal(context.Background(), true); err != nil {
			t.Fatalf("flow with AllowAnonymous invoked without a principal: got %v, want nil", err)
		}

		if err := requirePrincipal(WithPrincipal(context.Background(), types.Principal{Subject: "u"}), false); err != nil {
			t.Fatalf("flow invoked with a principal: got %v, want nil", err)
		}
	})

	t.Run("identity.no-principal-at-seam", func(t *testing.T) {
		// The seam refuses before any event or store access; the check takes
		// only the context, so nothing downstream can have been reached.
		if err := requirePrincipal(WithIdempotencyKey(context.Background(), "k"), false); !errors.Is(err, types.ErrNoPrincipal) {
			t.Fatalf("principal-less ctx with other values: got %v, want ErrNoPrincipal", err)
		}
	})
}
