package gohan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// resumeWorkerRT suspends once, then scripts the resumed run. Step 0 emits
// one delta; step 1 suspends the initial run; step 2 emits the resumed
// delta; step 3 blocks until its context or gate releases it.
type resumeWorkerRT struct {
	stallTestRT
	gate chan struct{}
}

func newResumeWorkerRT() *resumeWorkerRT {
	r := &resumeWorkerRT{gate: make(chan struct{})}
	r.steps = []func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error){
		func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
			return st, []types.Event{types.TextDelta{Turn: 0, Delta: "one"}}, runtime.Continue, nil
		},
		func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
			return st, nil, runtime.Continue, &types.SuspendError{Reason: types.Preempted}
		},
		func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
			return st, []types.Event{types.TextDelta{Turn: 0, Delta: "resumed"}}, runtime.Continue, nil
		},
		func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
			select {
			case <-r.gate:
				return st, nil, runtime.DoneStatus, nil
			case <-ctx.Done():
				return st, nil, runtime.Continue, &types.SuspendError{Reason: types.Preempted}
			}
		},
	}
	return r
}

// resumeStallConversation drives one Send to its suspension and returns the
// conversation plus the token a Resume consumes.
func resumeStallConversation(t *testing.T, rt *resumeWorkerRT, opts ...ConversationOption) (Conversation, *stallRuns, types.ResumeToken) {
	t.Helper()
	runs := &stallRuns{MemoryRuns: stores.NewMemoryRuns()}
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	stack := &Stack{stores: stores.Stores{SessionLog: log}}
	conv, err := NewConversation(stack, "chat", rt,
		append([]ConversationOption{
			WithConversationRuns(runs),
			WithConversationEventLog(events),
			WithConversationCheckpoints(stores.NewMemoryCheckpoints()),
		}, opts...)...)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	ctx := principalCtx(context.Background())
	var token types.ResumeToken
	n := 0
	for ev, err := range conv.Send(ctx, "sess-1", userMsg("hi")) {
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		n++
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if n != 2 || token == "" {
		t.Fatalf("send produced %d events, token %q; want the delta and a suspension", n, token)
	}
	return conv, runs, token
}

// suspensionSeq reports the sequence of the suspension the initial Send
// recorded, so a reattachment can resume behind that boundary and see the
// resumed run's own events.
func suspensionSeq(t *testing.T, conv Conversation, runID string) int64 {
	t.Helper()
	for e, err := range conv.(*conversation).events.Read(context.Background(), runID, 0) {
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		if _, ok := e.Payload.(types.Suspended); ok {
			return e.Meta.Seq
		}
	}
	t.Fatal("log holds no suspension")
	return 0
}

func TestResumeConsumerStallPreempts(t *testing.T) {
	const stall = 10 * time.Millisecond
	rt := newResumeWorkerRT()
	runs := &stallRuns{MemoryRuns: stores.NewMemoryRuns()}
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	stack := &Stack{
		stores: stores.Stores{SessionLog: log},
		limits: map[string]types.RunLimits{"chat": {ConsumerStall: stall}},
	}
	conv, err := NewConversation(stack, "chat", rt,
		WithConversationRuns(runs),
		WithConversationEventLog(events),
		WithConversationCheckpoints(stores.NewMemoryCheckpoints()),
	)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	ctx := principalCtx(context.Background())
	var token types.ResumeToken
	for ev, err := range conv.Send(ctx, "sess-1", userMsg("hi")) {
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if token == "" {
		t.Fatal("send produced no suspension")
	}

	release := make(chan struct{})
	streamDone := make(chan struct{})
	var got []types.Event
	go func() {
		defer close(streamDone)
		for ev, err := range conv.Resume(ctx, token, Continue()) {
			if err != nil {
				t.Errorf("resume: %v", err)
				return
			}
			got = append(got, ev)
			if _, ok := ev.(types.TextDelta); ok {
				<-release
			}
		}
	}()
	stallWait(t, "resume preemption persisted", func() bool {
		runs.mu.Lock()
		defer runs.mu.Unlock()
		return len(runs.suspended) == 2
	})
	close(release)
	<-streamDone

	if len(got) != 2 {
		t.Fatalf("resume delivered %d events, want the delta and the suspension", len(got))
	}
	s, ok := got[1].(types.Suspended)
	if !ok || s.Reason != types.Preempted || s.Token == "" || s.Token == token {
		t.Fatalf("resume ended with %v, want a fresh preempted token", got[1])
	}
	if runs.SessionLeaseActive(ctx, "sess-1") {
		t.Fatal("lease still active after the resumed run suspended")
	}
	for ev, err := range conv.Attach(ctx, runs.runID, 0) {
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
		if _, failed := ev.(types.TerminalError); failed {
			t.Fatalf("log holds a failure terminal for a preempted resume: %v", ev)
		}
	}
}

func TestResumeConsumerStallDetaches(t *testing.T) {
	const stall = 10 * time.Millisecond
	rt := newResumeWorkerRT()
	runs := &stallRuns{MemoryRuns: stores.NewMemoryRuns()}
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	stack := &Stack{
		stores: stores.Stores{SessionLog: log},
		limits: map[string]types.RunLimits{"chat": {ConsumerStall: stall}},
	}
	conv, err := NewConversation(stack, "chat", rt,
		WithConversationRuns(runs),
		WithConversationEventLog(events),
		WithConversationCheckpoints(stores.NewMemoryCheckpoints()),
		OnStall(StallDetach),
	)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	ctx := principalCtx(context.Background())
	var token types.ResumeToken
	for ev, err := range conv.Send(ctx, "sess-1", userMsg("hi")) {
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if token == "" {
		t.Fatal("send produced no suspension")
	}

	streamDone := make(chan struct{})
	var got []types.Event
	go func() {
		defer close(streamDone)
		for ev, err := range conv.Resume(ctx, token, Continue()) {
			if err != nil {
				t.Errorf("resume: %v", err)
				return
			}
			got = append(got, ev)
			if _, ok := ev.(types.TextDelta); ok {
				// The consumer walks away: delivery ends while the run
				// keeps recording into its log.
				return
			}
		}
	}()
	<-streamDone

	runs.mu.Lock()
	suspended := len(runs.suspended)
	runs.mu.Unlock()
	if suspended != 1 {
		t.Fatalf("run suspended %d times after the resume, want only the initial one", suspended)
	}
	if !runs.SessionLeaseActive(ctx, "sess-1") {
		t.Fatal("detached resume released its lease before finishing")
	}
	close(rt.gate)
	stallWait(t, "run end", func() bool {
		return !runs.SessionLeaseActive(ctx, "sess-1")
	})
	suspendedSeq := suspensionSeq(t, conv, runs.runID)
	// Replay stops at the suspension boundary the initial Send recorded, so
	// the reattachment resumes from the sequence behind it: the resumed
	// run's own events are what the client has not seen yet.
	var seen []types.Event
	dones := 0
	for ev, err := range conv.Attach(ctx, runs.runID, suspendedSeq) {
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
		if _, ok := ev.(types.Done); ok {
			dones++
		}
		seen = append(seen, ev)
	}
	if len(seen) != 2 || dones != 1 {
		t.Fatalf("log holds %d resumed events with %d dones, want the resumed delta and one Done", len(seen), dones)
	}
}

func TestResumeFailureRecordedForAttach(t *testing.T) {
	rt := newResumeWorkerRT()
	rt.steps[2] = func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
		return st, nil, runtime.Continue, errors.New("resume boom")
	}
	conv, runs, token := resumeStallConversation(t, rt)
	ctx := principalCtx(context.Background())
	suspendedSeq := suspensionSeq(t, conv, runs.runID)
	var errs []error
	dones := 0
	for ev, err := range conv.Resume(ctx, token, Continue()) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, ok := ev.(types.Done); ok {
			dones++
		}
	}
	if len(errs) != 1 {
		t.Fatalf("resume delivered %d error tuples, want exactly one live failure", len(errs))
	}
	if dones != 0 {
		t.Fatalf("resume delivered %d Done events after a failure, want 0", dones)
	}
	if runs.SessionLeaseActive(ctx, "sess-1") {
		t.Fatal("lease still active after the resumed run failed")
	}
	terminals := 0
	for ev, err := range conv.Attach(ctx, runs.runID, suspendedSeq) {
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
		if _, ok := ev.(types.TerminalError); ok {
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("log holds %d terminal errors, want exactly one durable failure", terminals)
	}
}
