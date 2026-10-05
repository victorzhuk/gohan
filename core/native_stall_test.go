package gohan

import (
	"context"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// stallBlockModel emits one text delta and then blocks on its gate, the
// way a slow provider keeps a run producing past the consumer's stall
// limit. Releasing the gate lets it finish.
type stallBlockModel struct {
	mu   sync.Mutex
	gate chan struct{}
}

func newStallBlockModel() *stallBlockModel {
	return &stallBlockModel{gate: make(chan struct{})}
}

func (m *stallBlockModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}
}

func (m *stallBlockModel) Generate(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		if !yield(types.ModelChunk{Kind: types.DeltaText, Delta: "one"}, nil) {
			return
		}
		select {
		case <-m.gate:
		case <-ctx.Done():
			return
		}
		yield(types.ModelChunk{Finish: types.FinishStop}, nil)
	}
}

func (m *stallBlockModel) release() {
	m.mu.Lock()
	defer m.mu.Unlock()
	close(m.gate)
}

func nativeStallLimits(stall time.Duration) types.RunLimits {
	l := nativeConvLimits(5)
	l.ConsumerStall = stall
	return l
}

func nativeStallStack(t *testing.T, model types.Model, limits types.RunLimits, convOpts ...ConversationOption) (*Stack, Conversation, *stallRuns) {
	t.Helper()
	runs := &stallRuns{MemoryRuns: stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)}
	stack, err := Build(
		WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        runs,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		}),
		WithModels(model),
		WithLimits("chat", limits),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "native",
			Tools:   []types.Tool{&bookTool{}, &nrrNoteTool{}},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	opts := append([]ConversationOption{
		WithConversationRuns(runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		WithConversationApprovalPolicy(nrrAllowPolicy{}),
	}, convOpts...)
	conv, err := NewNativeConversation(stack, "chat", opts...)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return stack, conv, runs
}

func TestNativeConsumerStallPreempts(t *testing.T) {
	const stall = 10 * time.Millisecond
	model := newStallBlockModel()
	_, conv, runs := nativeStallStack(t, model, nativeStallLimits(stall))
	m, ok := conv.(*conversation)
	if !ok || m.stall != stall {
		t.Fatalf("conversation stall limit = %v, want the resolved %v", m.stall, stall)
	}

	ctx := nrrCtx()
	streamDone := make(chan struct{})
	var got []types.Event
	go func() {
		defer close(streamDone)
		for ev, err := range conv.Send(ctx, "s1", nlUser("go")) {
			if err != nil {
				t.Errorf("send: %v", err)
				return
			}
			got = append(got, ev)
			if len(got) == 1 {
				// The consumer stops taking events past the stall limit:
				// the shipped guard preempts the run at its safe point.
				time.Sleep(4 * stall)
			}
		}
	}()
	<-streamDone

	if len(got) < 2 {
		t.Fatalf("delivered %d events, want at least the delta and the suspension", len(got))
	}
	if _, ok := got[0].(types.TextDelta); !ok {
		t.Fatalf("first event %T, want TextDelta", got[0])
	}
	for _, ev := range got[1 : len(got)-1] {
		if _, ok := ev.(types.Done); ok {
			t.Fatalf("Done delivered before the suspension: %v", ev)
		}
	}
	s, ok := got[len(got)-1].(types.Suspended)
	if !ok {
		t.Fatalf("last event %T, want Suspended", got[len(got)-1])
	}
	if s.Reason != types.Preempted || s.Token == "" {
		t.Fatalf("suspension reason %q token %q, want preempted with a token", s.Reason, s.Token)
	}
	runs.mu.Lock()
	suspended := len(runs.suspended)
	runs.mu.Unlock()
	if suspended != 1 {
		t.Fatalf("run suspended %d times, want 1", suspended)
	}
	if runs.SessionLeaseActive(ctx, "s1") {
		t.Fatal("lease still active after the preempted run suspended")
	}
	for ev, err := range conv.Attach(ctx, runs.runID, 0) {
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
		if _, ok := ev.(types.Done); ok {
			t.Fatalf("log holds a Done for a preempted run: %v", ev)
		}
		if _, failed := ev.(types.TerminalError); failed {
			t.Fatalf("log holds a failure terminal for a preempted run: %v", ev)
		}
	}
}

func TestNativeConsumerStallDetaches(t *testing.T) {
	const stall = 10 * time.Millisecond
	model := newStallBlockModel()
	_, conv, runs := nativeStallStack(t, model, nativeStallLimits(stall), OnStall(StallDetach))

	ctx := nrrCtx()
	stalled := make(chan struct{})
	release := make(chan struct{})
	streamDone := make(chan struct{})
	var got []types.Event
	go func() {
		defer close(streamDone)
		for ev, err := range conv.Send(ctx, "s1", nlUser("go")) {
			if err != nil {
				t.Errorf("send: %v", err)
				return
			}
			got = append(got, ev)
			if len(got) == 1 {
				close(stalled)
				// The consumer walks away: delivery ends while the run
				// keeps recording into its log.
				<-release
			}
		}
	}()
	<-stalled
	time.Sleep(4 * stall)
	close(release)
	<-streamDone

	if len(got) != 1 {
		t.Fatalf("delivery sent %d events, want only the first delta", len(got))
	}
	if _, ok := got[0].(types.TextDelta); !ok {
		t.Fatalf("first event %T, want TextDelta", got[0])
	}
	runs.mu.Lock()
	suspended := len(runs.suspended)
	runs.mu.Unlock()
	if suspended != 0 {
		t.Fatalf("run suspended %d times, want 0", suspended)
	}
	if !runs.SessionLeaseActive(ctx, "s1") {
		t.Fatal("lease released before the detached run finished")
	}

	// The detached run continues: releasing the gate lets it finish.
	model.release()
	stallWait(t, "run end", func() bool {
		return !runs.SessionLeaseActive(ctx, "s1")
	})
	var missed []types.Event
	for ev, err := range conv.Attach(ctx, runs.runID, 0) {
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
		missed = append(missed, ev)
	}
	if len(missed) < 2 {
		t.Fatalf("log holds %d events, want the delta and the Done", len(missed))
	}
	if _, ok := missed[0].(types.TextDelta); !ok {
		t.Fatalf("first logged event %T, want TextDelta", missed[0])
	}
	dones := 0
	for _, ev := range missed[1:] {
		if _, ok := ev.(types.Done); ok {
			dones++
		}
	}
	if dones != 1 {
		t.Fatalf("log holds %d Done events, want 1", dones)
	}
}
