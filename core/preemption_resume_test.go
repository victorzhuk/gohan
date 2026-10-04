package gohan

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// preemptConvRT scripts a two-turn conversation run: turn one appends and
// completes, turn two blocks on a gate and then honours the driver's
// cancellation, the way a real call observes it.
type preemptConvRT struct {
	log      *appendSpy
	gate     chan struct{}
	entered2 chan struct{}
	once2    sync.Once
	step2    context.Context

	mu    sync.Mutex
	steps int
}

// cancelled reports the running context of the gated step; the test waits
// on it to know the driver's cancellation landed before opening the gate.
func (r *preemptConvRT) cancelled() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.step2 == nil {
		return nil
	}
	return r.step2.Done()
}

func (r *preemptConvRT) Name() string                         { return "preempt.conv" }
func (r *preemptConvRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *preemptConvRT) Start(_ context.Context, _ runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *preemptConvRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	r.steps++
	r.mu.Unlock()
	if r.steps > 1 && r.gate != nil {
		r.mu.Lock()
		r.step2 = ctx
		r.mu.Unlock()
		if r.entered2 != nil {
			r.once2.Do(func() { close(r.entered2) })
		}
		<-r.gate
		if err := ctx.Err(); err != nil {
			return st, nil, runtime.Continue, err
		}
	}
	if r.steps > 1 {
		return st, nil, runtime.DoneStatus, nil
	}
	turn := st.Turn + 1
	asst := types.Message{
		ID: assistantID(turn), Role: types.RoleAssistant,
		Blocks: []types.Block{types.Text{Text: "turn one"}},
	}
	v, err := r.log.Append(ctx, st.HistoryVersion, asst)
	if err != nil {
		return st, nil, runtime.Continue, err
	}
	ev := types.AssistantMessage{Turn: turn, Message: asst}
	return runtime.State{Turn: turn, HistoryVersion: v}, []types.Event{ev}, runtime.Continue, nil
}

func preemptConvSetup(t *testing.T, rt runtime.Runtime, log stores.SessionLog) Conversation {
	t.Helper()
	stack, err := Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	stack.stores = stores.Stores{SessionLog: log}
	runs := &runsFixture{
		MemoryRuns: stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		),
		onSess: map[string]string{},
	}
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	conv, err := NewConversation(stack, "chat", rt,
		WithConversationRuns(runs),
		WithConversationEventLog(events),
		WithConversationCheckpoints(stores.NewMemoryCheckpoints()),
	)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	return conv
}

func TestPreemptedResumeContinue(t *testing.T) {
	ctx := principalCtx(context.Background())

	t.Run("runtime.preempted-resume-continue", func(t *testing.T) {
		log := &appendSpy{}
		inner := &preemptConvRT{log: log, gate: make(chan struct{}), entered2: make(chan struct{})}
		pre := NewPreemptor()
		conv := preemptConvSetup(t, pre.Runtime(inner), stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
		firstCh := make(chan types.Event, 1)
		res := streamCollect(func(yield func(types.Event, error) bool) {
			first := true
			for ev, err := range conv.Send(ctx, "sess-pre", userMsg("hi")) {
				if first && ev != nil {
					firstCh <- ev
					first = false
				}
				if !yield(ev, err) {
					return
				}
			}
		})
		first := <-firstCh
		if _, isAsst := first.(types.AssistantMessage); !isAsst {
			t.Fatalf("first event %T, want AssistantMessage", first)
		}
		<-inner.entered2
		pre.Preempt()
		<-inner.cancelled()
		close(inner.gate)
		out := <-res
		if out.err != nil {
			t.Fatalf("send: %v", out.err)
		}
		sus, isSus := lastOf(out.evs).(types.Suspended)
		if !isSus || sus.Reason != types.Preempted {
			t.Fatalf("last event %v, want Suspended{preempted}", lastOf(out.evs))
		}
		appendsBeforeResume := log.count()

		continued := collectStream(ResumePreempted(ctx, conv, sus.Token))
		if continued.err != nil {
			t.Fatalf("resume: %v", continued.err)
		}
		done, isDone := lastOf(continued.evs).(types.Done)
		if !isDone || done.Reason != types.StopCompleted {
			t.Fatalf("last event %v, want Done{completed}", lastOf(continued.evs))
		}
		for _, ev := range continued.evs {
			if g, ok := ev.(types.GuardBlocked); ok {
				t.Fatalf("resumed run hit the gate: %+v", g)
			}
		}
		if got := log.count(); got != appendsBeforeResume {
			t.Fatalf("resume appended %d messages, want none: no approver input", got-appendsBeforeResume)
		}
	})
}
