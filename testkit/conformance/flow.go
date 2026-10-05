// Package conformance holds the suites every adapter and driver build runs
// against its capability: a scripted capability on memory stores, the
// observable event sequence asserted, nothing left behind.
package conformance

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

var flowProfile = types.ModelProfile{Version: "conformance-flow"}

// FlowRuntime is a scripted stepper for the flow suite: each Step drains
// one turn from the scripted model, emits it as one assistant message and
// either finishes, or, at the configured step, suspends with a human
// approval request. A gate armed through Gate blocks the first Step on a
// channel so a test can cancel mid-run; Entered closes once that Step
// begins.
type FlowRuntime struct {
	model     types.Model
	suspendAt int

	gate    chan struct{}
	entered chan struct{}
	once    sync.Once

	mu    sync.Mutex
	calls int
}

// NewFlowRuntime builds the stepper. suspendAt is the one-based step the
// run suspends on; zero means the run never suspends.
func NewFlowRuntime(model types.Model, suspendAt int) *FlowRuntime {
	return &FlowRuntime{model: model, suspendAt: suspendAt}
}

// Entered returns the channel that closes when the first Step begins.
func (rt *FlowRuntime) Entered() <-chan struct{} { return rt.entered }

// Release unblocks the gated Step. It is a no-op without a gate.
func (rt *FlowRuntime) Release() {
	if rt.gate != nil {
		close(rt.gate)
	}
}

// Gate arms the blocking first Step.
func (rt *FlowRuntime) Gate() {
	rt.gate = make(chan struct{})
	rt.entered = make(chan struct{})
}

// Name implements runtime.Runtime.
func (rt *FlowRuntime) Name() string { return "conformance.flow" }

// Granularity implements runtime.Runtime.
func (rt *FlowRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

// Start implements runtime.Stepper.
func (rt *FlowRuntime) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

// Step implements runtime.Stepper. The resumed run arrives with the
// checkpointed turn number and drives to completion; a run reaching its
// suspend step returns a SuspendError carrying the approval request.
func (rt *FlowRuntime) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if rt.gate != nil {
		rt.once.Do(func() { close(rt.entered) })
		<-rt.gate
	}
	rt.mu.Lock()
	rt.calls++
	rt.mu.Unlock()

	var text strings.Builder
	for chunk, err := range rt.model.Generate(ctx, types.ModelRequest{}) {
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		text.WriteString(chunk.Delta)
	}
	next := st
	next.Turn = st.Turn + 1
	evs := []types.Event{assistantMessage(st.Turn, text.String())}
	if rt.suspendAt == 0 || rt.calls != rt.suspendAt {
		if rt.calls < rt.suspendAt {
			return next, evs, runtime.Continue, nil
		}
		return next, evs, runtime.DoneStatus, nil
	}
	return st, evs, runtime.Continue, &types.SuspendError{Reason: types.HumanApproval}
}

func assistantMessage(turn int, text string) types.AssistantMessage {
	return types.AssistantMessage{
		Turn: turn,
		Message: types.Message{
			Role: types.RoleAssistant,
			Blocks: []types.Block{types.Text{
				BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}},
				Text:      text,
			}},
		},
	}
}

// FlowFixture wires one flow run end to end on memory stores: the scripted
// runtime and model, the session log the run appends through, the runs and
// event stores the driver leases and records against, and the checkpoints
// store Resume consumes tokens through.
type FlowFixture struct {
	Conversation gohan.Conversation
	Runtime      *FlowRuntime
	Model        *gohantest.ScriptedModel
	Runs         *stores.MemoryRuns
	Events       *stores.MemoryEventLog
	Log          stores.SessionLog
	Checkpoints  *stores.MemoryCheckpoints

	Store *flowRuns
}

// flowRuns wraps the memory runs store for the fixture: it answers the
// SessionRunFinder port Cancel uses for a run another pod started, and
// closes CancelQueued once a cancel signal is in a run's mailbox, so a
// test can order the cancel against the run it targets.
type flowRuns struct {
	*stores.MemoryRuns

	mu     sync.Mutex
	onSess map[string]string

	cancelQueued chan struct{}
}

func (f *flowRuns) Start(ctx context.Context, r stores.Run, ttl time.Duration) (stores.Lease, error) {
	lease, err := f.MemoryRuns.Start(ctx, r, ttl)
	if err == nil {
		f.mu.Lock()
		f.onSess[r.SessionID] = r.RunID
		f.mu.Unlock()
	}
	return lease, err
}

func (f *flowRuns) Signal(ctx context.Context, runID string, sig stores.Signal) error {
	if err := f.MemoryRuns.Signal(ctx, runID, sig); err != nil {
		return err
	}
	if sig.Kind == stores.SignalCancel {
		f.closeCancel()
	}
	return nil
}

func (f *flowRuns) closeCancel() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancelQueued != nil {
		close(f.cancelQueued)
		f.cancelQueued = nil
	}
}

func (f *flowRuns) CancelQueued() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelQueued
}

// RunForSession implements stores.SessionRunFinder.
func (f *flowRuns) RunForSession(_ context.Context, sessionID string) (stores.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	runID, ok := f.onSess[sessionID]
	if !ok {
		return stores.Run{}, stores.ErrRunNotFound
	}
	return stores.Run{SessionID: sessionID, RunID: runID}, nil
}

// NewFlowFixture builds the fixture. suspendAt follows NewFlowRuntime; the
// turns replay on the scripted model in call order.
func NewFlowFixture(t *testing.T, suspendAt int, turns ...gohantest.Turn) *FlowFixture {
	t.Helper()
	model := gohantest.NewScriptedModel(flowProfile, turns...)
	rt := NewFlowRuntime(model, suspendAt)
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	memRuns := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(runInfoFromCtx),
	)
	runs := &flowRuns{
		MemoryRuns:   memRuns,
		onSess:       map[string]string{},
		cancelQueued: make(chan struct{}),
	}
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	cps := stores.NewMemoryCheckpoints(
		stores.WithMemoryCheckpointClock(time.Now),
		stores.WithMemoryCheckpointRunInfo(runInfoFromCtx),
	)
	stack, err := gohan.Build(gohan.WithStores(stores.Stores{SessionLog: log}))
	if err != nil {
		t.Fatalf("build stack: %v", err)
	}
	conv, err := gohan.NewConversation(stack, "flow", rt,
		gohan.WithConversationRuns(runs),
		gohan.WithConversationEventLog(events),
		gohan.WithConversationCheckpoints(cps),
	)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	return &FlowFixture{
		Conversation: conv,
		Runtime:      rt,
		Model:        model,
		Runs:         memRuns,
		Store:        runs,
		Events:       events,
		Log:          log,
		Checkpoints:  cps,
	}
}

func runInfoFromCtx(ctx context.Context) (types.RunInfo, bool) {
	p, ok := types.PrincipalFrom(ctx)
	return types.RunInfo{Principal: p}, ok
}

// FlowUserMessage builds the user message the suite sends.
func FlowUserMessage(text string) types.Message {
	return types.Message{
		Role: types.RoleUser,
		Blocks: []types.Block{types.Text{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}},
			Text:      text,
		}},
	}
}

// Flow runs the flow conformance suite: plain invocation, suspension and
// resume, cancellation from outside the run, and the goroutine-leak check
// over a full suspend-and-resume cycle. It asserts the observable event
// sequence only.
func Flow(t *testing.T) {
	t.Run("plain invoke", func(t *testing.T) {
		f := NewFlowFixture(t, 0, gohantest.Text("hello"))
		evs := flowStream(t, f.Conversation.Send(flowCtx(), "sess", FlowUserMessage("hi")))
		assertDone(t, evs, types.StopCompleted)
		assertAssistantText(t, evs, "hello")
		if got := len(f.Model.Requests()); got != 1 {
			t.Fatalf("model called %d times, want 1", got)
		}
	})

	t.Run("suspend and resume", func(t *testing.T) {
		f := NewFlowFixture(t, 1, gohantest.Text("needs approval"), gohantest.Text("granted"))
		evs := flowStream(t, f.Conversation.Send(flowCtx(), "sess", FlowUserMessage("hi")))
		susp, ok := evs[len(evs)-1].(types.Suspended)
		if !ok {
			t.Fatalf("last event %T, want types.Suspended", evs[len(evs)-1])
		}
		if susp.Reason != types.HumanApproval || susp.Token == "" {
			t.Fatalf("suspension: reason %q token %q, want %q and a token", susp.Reason, susp.Token, types.HumanApproval)
		}
		for _, e := range evs {
			if _, ok := e.(types.Done); ok {
				t.Fatal("run emitted Done on suspension")
			}
		}

		resumed := flowStream(t, f.Conversation.Resume(flowCtx(), susp.Token, gohan.Approve()))
		assertAssistantText(t, resumed, "granted")
		assertDone(t, resumed, types.StopCompleted)

		if err := flowErr(t, f.Conversation.Resume(flowCtx(), susp.Token, gohan.Approve())); !errors.Is(err, types.ErrTokenConsumed) {
			t.Fatalf("reused token: got %v, want types.ErrTokenConsumed", err)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		f := NewFlowFixture(t, 0, gohantest.Text("hello"))
		f.Runtime.Gate()
		sent := make(chan []types.Event, 1)
		go func() { sent <- flowStream(t, f.Conversation.Send(flowCtx(), "sess", FlowUserMessage("hi"))) }()
		<-f.Runtime.Entered()
		cancelled := make(chan error, 1)
		go func() { cancelled <- f.Conversation.Cancel(flowCtx(), "sess") }()
		<-f.Store.CancelQueued()
		f.Runtime.Release()
		if err := <-cancelled; err != nil {
			t.Fatalf("cancel: %v", err)
		}
		evs := <-sent
		assertDone(t, evs, types.StopCancelled)
		if f.Runs.SessionLeaseActive(flowCtx(), "sess") {
			t.Fatal("run still holds the lease after cancel")
		}
	})

	t.Run("leak check", func(t *testing.T) {
		gohantest.LeakCheck(t, func() {
			f := NewFlowFixture(t, 1, gohantest.Text("needs approval"), gohantest.Text("granted"))
			evs := flowStream(t, f.Conversation.Send(flowCtx(), "sess", FlowUserMessage("hi")))
			susp := evs[len(evs)-1].(types.Suspended)
			resumed := flowStream(t, f.Conversation.Resume(flowCtx(), susp.Token, gohan.Approve()))
			assertDone(t, resumed, types.StopCompleted)
		})
	})
}

func flowCtx() context.Context {
	return gohan.WithPrincipal(context.Background(), types.Principal{Tenant: "t-a", Subject: "u1"})
}

func flowStream(t testing.TB, seq func(func(types.Event, error) bool)) []types.Event {
	t.Helper()
	var evs []types.Event
	for e, err := range seq {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		evs = append(evs, e)
	}
	return evs
}

func flowErr(t testing.TB, seq func(func(types.Event, error) bool)) error {
	t.Helper()
	var first error
	for e, err := range seq {
		if err != nil {
			first = err
			continue
		}
		if e != nil {
			t.Fatalf("unexpected event %T after error", e)
		}
	}
	return first
}

func assertDone(t testing.TB, evs []types.Event, want types.StopReason) {
	t.Helper()
	last, ok := evs[len(evs)-1].(types.Done)
	if !ok {
		t.Fatalf("last event %T, want types.Done", evs[len(evs)-1])
	}
	if last.Reason != want {
		t.Fatalf("Done reason %q, want %q", last.Reason, want)
	}
}

func assertAssistantText(t testing.TB, evs []types.Event, want string) {
	t.Helper()
	for _, e := range evs {
		am, ok := e.(types.AssistantMessage)
		if !ok {
			continue
		}
		for _, b := range am.Message.Blocks {
			if tb, ok := b.(types.Text); ok && tb.Text == want {
				return
			}
		}
	}
	t.Fatalf("no assistant message with text %q in %d events", want, len(evs))
}
