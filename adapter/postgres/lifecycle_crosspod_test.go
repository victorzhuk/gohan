//go:build full

package postgres_test

import (
	"context"
	"errors"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/adapter/postgres"
	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// crosspodRT blocks after its first step until the test closes the gate,
// so the run holds a live Postgres lease while the other pod calls in.
type crosspodRT struct {
	mu      sync.Mutex
	entered chan struct{}
	gate    chan struct{}
	calls   int
	// ver reports the session's stored history version, the way a
	// plain-runtime conversation's turn appends expect.
	ver func() int64
}

func (r *crosspodRT) Name() string                         { return "crosspod.test" }
func (r *crosspodRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *crosspodRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *crosspodRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	if call == 1 {
		if r.entered != nil {
			close(r.entered)
		}
		return st, []types.Event{types.TextDelta{Delta: "d0"}}, runtime.Continue, nil
	}
	if r.gate != nil {
		if r.ver != nil {
			st.HistoryVersion = r.ver()
		}
		<-r.gate
	}
	return st, nil, runtime.DoneStatus, nil
}

// lifecyclePG wires two conversation-ready stacks over one real Postgres
// Runs store and one real Postgres SessionLog, the sharing a second pod
// would see. Each conversation keeps its own in-memory event log.
type lifecyclePG struct {
	db       *postgres.DB
	runs     *postgres.Runs
	sessions *postgres.SessionLog
	stack    *gohan.Stack
	owner    context.Context
	foreign  context.Context
}

func newLifecyclePG(t *testing.T, name string) lifecyclePG {
	t.Helper()
	db := newDB(t, bindSchema(name))
	runs := postgres.NewRuns(db, postgres.WithRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
		return types.RunInfo{Principal: bindPrincipal}, true
	}))
	sessions := postgres.NewSessionLog(db, postgres.WithSessionPrincipals(types.PrincipalFrom))
	stack, err := gohan.Build(gohan.WithStores(stores.Stores{SessionLog: sessions}))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return lifecyclePG{
		db:       db,
		runs:     runs,
		sessions: sessions,
		stack:    stack,
		owner:    types.WithPrincipal(context.Background(), bindPrincipal),
		foreign:  types.WithPrincipal(context.Background(), types.Principal{Tenant: "t-b", Subject: "u2"}),
	}
}

func (f lifecyclePG) version() func() int64 {
	return func() int64 {
		h, err := f.sessions.Load(f.owner, "s1")
		if err != nil {
			return 0
		}
		return h.Version
	}
}

func (f lifecyclePG) conversation(t *testing.T, rt runtime.Runtime, opts ...gohan.ConversationOption) gohan.Conversation {
	t.Helper()
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	opts = append([]gohan.ConversationOption{
		gohan.WithConversationRuns(f.runs),
		gohan.WithConversationEventLog(events),
	}, opts...)
	conv, err := gohan.NewConversation(f.stack, "chat", rt, opts...)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	return conv
}

type crosspodResult struct {
	evs []types.Event
	err error
}

func collectCrosspod(seq iter.Seq2[types.Event, error]) crosspodResult {
	var res crosspodResult
	for e, err := range seq {
		if err != nil {
			res.err = err
			return res
		}
		res.evs = append(res.evs, e)
	}
	return res
}

func crosspodUserMsg(text string) types.Message {
	return types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: text}}}
}

func crosspodWaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func lastEvent(evs []types.Event) types.Event {
	if len(evs) == 0 {
		return nil
	}
	return evs[len(evs)-1]
}

func TestLifecycleCrossPod(t *testing.T) {
	t.Run("streams.active-run-cross-instance", func(t *testing.T) {
		f := newLifecyclePG(t, "lifecycle_xp_active")
		rt := &crosspodRT{entered: make(chan struct{}), gate: make(chan struct{}), ver: f.version()}
		owner := f.conversation(t, rt)
		res := make(chan crosspodResult, 1)
		go func() { res <- collectCrosspod(owner.Send(f.owner, "s1", crosspodUserMsg("hi"))) }()
		<-rt.entered

		// The owner pod captures the run id through the public handle
		// while the lease is live, before the gate closes.
		captured, err := owner.ActiveRun(f.owner, "s1")
		if err != nil || captured == "" {
			t.Fatalf("active run before gate = (%q, %v)", captured, err)
		}

		// A second conversation on another pod shares only the Postgres
		// stores; its live map is empty, so discovery rides the finder.
		other := f.conversation(t, &crosspodRT{})
		id, err := other.ActiveRun(f.owner, "s1")
		if err != nil {
			t.Fatalf("cross-instance active run: %v", err)
		}
		if id != captured {
			t.Fatalf("cross-instance id %q, want %q", id, captured)
		}

		close(rt.gate)
		got := <-res
		if got.err != nil {
			t.Fatalf("send: %v", got.err)
		}
		if done, ok := lastEvent(got.evs).(types.Done); !ok || done.Reason != types.StopCompleted {
			t.Fatalf("last event %#v, want Done{completed}", lastEvent(got.evs))
		}
	})

	t.Run("stores.run-for-session-cross-instance", func(t *testing.T) {
		f := newLifecyclePG(t, "lifecycle_xp_cancel")
		rt := &crosspodRT{entered: make(chan struct{}), gate: make(chan struct{}), ver: f.version()}
		owner := f.conversation(t, rt)
		res := make(chan crosspodResult, 1)
		go func() { res <- collectCrosspod(owner.Send(f.owner, "s1", crosspodUserMsg("hi"))) }()
		<-rt.entered
		if _, err := owner.ActiveRun(f.owner, "s1"); err != nil {
			t.Fatalf("active run before gate: %v", err)
		}

		// The other pod's Cancel resolves the session's run through the
		// shared Postgres runs store and signals it. Cancel returns once
		// the store shows the run finished, so the gate closes while it
		// polls instead of after the full lease TTL.
		other := f.conversation(t, &crosspodRT{})
		go func() {
			time.Sleep(100 * time.Millisecond)
			close(rt.gate)
		}()
		if err := other.Cancel(f.owner, "s1"); err != nil {
			t.Fatalf("cross-pod cancel: %v", err)
		}
		got := <-res
		if got.err != nil {
			t.Fatalf("send: %v", got.err)
		}
		if done, ok := lastEvent(got.evs).(types.Done); !ok || done.Reason != types.StopCancelled {
			t.Fatalf("last event %#v, want Done{cancelled}", lastEvent(got.evs))
		}
		// The run is terminal afterwards: no id is disclosed.
		id, err := owner.ActiveRun(f.owner, "s1")
		if !errors.Is(err, types.ErrRunNotActive) || id != "" {
			t.Fatalf("after cancel = (%q, %v), want ErrRunNotActive with no id", id, err)
		}
	})

	t.Run("cross-pod steer reaches the owner run", func(t *testing.T) {
		f := newLifecyclePG(t, "lifecycle_xp_steer")
		rt := &crosspodRT{entered: make(chan struct{}), gate: make(chan struct{}), ver: f.version()}
		owner := f.conversation(t, rt)
		res := make(chan crosspodResult, 1)
		go func() { res <- collectCrosspod(owner.Send(f.owner, "s1", crosspodUserMsg("hi"))) }()
		<-rt.entered

		other := f.conversation(t, &crosspodRT{})
		steer := crosspodUserMsg("one more")
		steer.ID = "steer-xp"
		if err := other.Steer(f.owner, "s1", steer); err != nil {
			t.Fatalf("cross-pod steer: %v", err)
		}
		close(rt.gate)
		got := <-res
		if got.err != nil {
			t.Fatalf("send: %v", got.err)
		}
		// Finish refuses while the steer is pending; the drain applies it
		// and the run reports one more turn before the final Done.
		applied := false
		for _, ev := range got.evs {
			if sa, ok := ev.(types.SteerApplied); ok {
				applied = true
				if sa.MessageID != "steer-xp" {
					t.Fatalf("SteerApplied.MessageID = %q, want steer-xp", sa.MessageID)
				}
			}
		}
		if !applied {
			t.Fatalf("no SteerApplied in %d events", len(got.evs))
		}
		if done, ok := lastEvent(got.evs).(types.Done); !ok || done.Reason != types.StopCompleted {
			t.Fatalf("last event %#v, want Done{completed}", lastEvent(got.evs))
		}
	})

	t.Run("streams.active-run-owner-authorized", func(t *testing.T) {
		f := newLifecyclePG(t, "lifecycle_xp_owner")
		rt := &crosspodRT{entered: make(chan struct{}), gate: make(chan struct{}), ver: f.version()}
		owner := f.conversation(t, rt)
		res := make(chan crosspodResult, 1)
		go func() { res <- collectCrosspod(owner.Send(f.owner, "s1", crosspodUserMsg("hi"))) }()
		<-rt.entered

		// A foreign tenant gets no id and no control: ActiveRun, Cancel
		// and Steer all refuse before the run is touched.
		other := f.conversation(t, &crosspodRT{})
		id, err := other.ActiveRun(f.foreign, "s1")
		if !errors.Is(err, types.ErrSessionForbidden) || id != "" {
			t.Fatalf("active run = (%q, %v), want ErrSessionForbidden with no id", id, err)
		}
		if err := other.Cancel(f.foreign, "s1"); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("cancel err = %v, want ErrSessionForbidden", err)
		}
		if err := other.Steer(f.foreign, "s1", crosspodUserMsg("no")); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("steer err = %v, want ErrSessionForbidden", err)
		}

		close(rt.gate)
		got := <-res
		if got.err != nil {
			t.Fatalf("send: %v", got.err)
		}
		for _, ev := range got.evs {
			if _, ok := ev.(types.SteerApplied); ok {
				t.Fatalf("steer applied despite the foreign refusal: %#v", ev)
			}
		}
		if done, ok := lastEvent(got.evs).(types.Done); !ok || done.Reason != types.StopCompleted {
			t.Fatalf("last event %#v, want Done{completed} untouched by the foreign calls", lastEvent(got.evs))
		}
	})

	t.Run("streams.detached-run-discoverable", func(t *testing.T) {
		f := newLifecyclePG(t, "lifecycle_xp_detached")
		rt := &crosspodRT{entered: make(chan struct{}), gate: make(chan struct{}), ver: f.version()}
		owner := f.conversation(t, rt, gohan.Detached())
		go func() { collectCrosspod(owner.Send(f.owner, "s1", crosspodUserMsg("hi"))) }()
		<-rt.entered

		// Send already returned; the detached run holds a live lease and
		// the owner captures its id through the public handle.
		captured, err := owner.ActiveRun(f.owner, "s1")
		if err != nil || captured == "" {
			t.Fatalf("detached active run = (%q, %v)", captured, err)
		}

		// The other pod discovers the same run.
		other := f.conversation(t, &crosspodRT{})
		id, err := other.ActiveRun(f.owner, "s1")
		if err != nil || id != captured {
			t.Fatalf("cross-instance detached id = (%q, %v), want %q", id, err, captured)
		}

		close(rt.gate)
		crosspodWaitFor(t, "detached run to finish", func() bool {
			_, err := owner.ActiveRun(f.owner, "s1")
			return errors.Is(err, types.ErrRunNotActive)
		})
	})
}
