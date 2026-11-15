package loadtest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestFailNextFailsExactlyN(t *testing.T) {
	j := stores.NewMemoryJournal()
	f := NewFaults()
	w := Wrap(stores.Stores{Journal: j}, f, PortJournal).Journal

	f.FailNext(PortJournal, 2)
	key := types.CallKey{SessionID: "s", CallID: "c"}
	for i := 0; i < 2; i++ {
		_, _, err := w.Reserve(context.Background(), key, "tool", "fp", nil)
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("call %d: want ErrUnavailable, got %v", i+1, err)
		}
	}
	entry, _, err := w.Reserve(context.Background(), key, "tool", "fp", nil)
	if err != nil {
		t.Fatalf("third call: want success, got entry=%v err=%v", entry, err)
	}
	if got := f.Operations(PortJournal); got != 3 {
		t.Fatalf("Operations = %d, want 3", got)
	}
	if got := f.Failures(PortJournal); got != 2 {
		t.Fatalf("Failures = %d, want 2", got)
	}
}

func TestFailRange(t *testing.T) {
	el := stores.NewMemoryEventLog()
	f := NewFaults()
	w := Wrap(stores.Stores{EventLog: el}, f, PortEventLog).EventLog

	f.FailRange(PortEventLog, 2, 2)
	ctx := context.Background()
	ev := stores.Event{Payload: types.TextDelta{Delta: "x"}}
	for op := 1; op <= 4; op++ {
		err := w.Append(ctx, "run", ev)
		wantFail := op == 2 || op == 3
		if wantFail != errors.Is(err, ErrUnavailable) {
			t.Fatalf("op %d: fail=%v err=%v", op, wantFail, err)
		}
	}
	if f.Failures(PortEventLog) != 2 {
		t.Fatalf("Failures = %d, want 2", f.Failures(PortEventLog))
	}
}

func TestFailAllAndClear(t *testing.T) {
	el := stores.NewMemoryEventLog()
	f := NewFaults()
	w := Wrap(stores.Stores{EventLog: el}, f, PortEventLog).EventLog

	ctx := context.Background()
	f.FailAll(PortEventLog)
	for i := 0; i < 3; i++ {
		if err := w.Append(ctx, "run", stores.Event{Payload: types.TextDelta{Delta: "x"}}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("call %d: want ErrUnavailable, got %v", i+1, err)
		}
	}
	f.Clear(PortEventLog)
	if err := w.Append(ctx, "run", stores.Event{Payload: types.TextDelta{Delta: "x"}}); err != nil {
		t.Fatalf("after Clear: want success, got %v", err)
	}
	// Clear removed the schedule; the post-Clear Append is the only op left.
	if f.Failures(PortEventLog) != 0 || f.Operations(PortEventLog) != 1 {
		t.Fatalf("counters after Clear+Append: failures=%d ops=%d, want 0/1", f.Failures(PortEventLog), f.Operations(PortEventLog))
	}
}

func TestConcurrentCounts(t *testing.T) {
	sl := stores.NewMemorySessionLog()
	f := NewFaults()
	w := Wrap(stores.Stores{SessionLog: sl}, f, PortSessionLog).SessionLog

	f.FailAll(PortSessionLog)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_, _ = w.Load(context.Background(), "s")
			}
		}()
	}
	wg.Wait()
	if got := f.Operations(PortSessionLog); got != 400 {
		t.Fatalf("Operations = %d, want 400", got)
	}
	if got := f.Failures(PortSessionLog); got != 400 {
		t.Fatalf("Failures = %d, want 400", got)
	}
}

func TestRunDelegation(t *testing.T) {
	mr := stores.NewMemoryRuns()
	f := NewFaults()
	w := Wrap(stores.Stores{Runs: mr}, f, PortRuns).Runs

	run := stores.Run{RunID: "r1", SessionID: "s1", State: stores.Running}
	lease, err := w.Start(context.Background(), run, time.Minute)
	if err != nil || lease.RunID != "r1" || lease.Generation != 1 {
		t.Fatalf("Start: lease=%+v err=%v", lease, err)
	}
	hb, err := w.Heartbeat(context.Background(), lease)
	if err != nil || hb.Generation < lease.Generation {
		t.Fatalf("Heartbeat: lease=%+v err=%v", hb, err)
	}
}

func TestJournalReplayDelegation(t *testing.T) {
	j := stores.NewMemoryJournal()
	f := NewFaults()
	w := Wrap(stores.Stores{Journal: j}, f, PortJournal).Journal

	ctx := context.Background()
	key := types.CallKey{SessionID: "s", CallID: "c"}
	if _, replayed, err := w.Reserve(ctx, key, "tool", "fp", nil); err != nil || !replayed {
		t.Fatalf("first Reserve: replayed=%v err=%v", replayed, err)
	}
	_, replayed, err := w.Reserve(ctx, key, "tool", "fp", nil)
	if err != nil || replayed {
		t.Fatalf("second Reserve: replayed=%v err=%v", replayed, err)
	}
}

func TestEventLogReadStreams(t *testing.T) {
	el := stores.NewMemoryEventLog()
	f := NewFaults()
	w := Wrap(stores.Stores{EventLog: el}, f, PortEventLog).EventLog

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := w.Append(ctx, "run", stores.Event{Payload: types.TextDelta{Delta: "hi"}}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	var got int
	for _, err := range w.Read(ctx, "run", 0) {
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		got++
	}
	if got != 3 {
		t.Fatalf("Read streamed %d events, want 3", got)
	}

	f.FailAll(PortEventLog)
	n := 0
	for _, err := range w.Read(ctx, "run", 0) {
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Read under outage: want ErrUnavailable, got %v", err)
		}
		n++
	}
	if n != 1 {
		t.Fatalf("outage Read yielded %d times, want 1", n)
	}
}

func TestOptionalInterfacesSatisfied(t *testing.T) {
	f := NewFaults()
	wr := Wrap(stores.Stores{Runs: stores.NewMemoryRuns()}, f, PortRuns).Runs
	if _, ok := wr.(stores.PreemptedLister); !ok {
		t.Fatal("wrapped runs does not satisfy PreemptedLister")
	}
	pre, err := wr.(stores.PreemptedLister).Preempted(context.Background(), 10)
	if err != nil || len(pre) != 0 {
		t.Fatalf("Preempted delegation: %v %v", pre, err)
	}

	wc := Wrap(stores.Stores{Checkpoints: stores.NewMemoryCheckpoints()}, f, PortCheckpoints).Checkpoints
	if _, ok := wc.(stores.ResumeReadyLister); !ok {
		t.Fatal("wrapped checkpoints does not satisfy ResumeReadyLister")
	}
	if _, err := wc.(stores.ResumeReadyLister).ResumeReady(context.Background(), 10); errors.Is(err, ErrUnavailable) {
		t.Fatalf("ResumeReady delegation hit injected outage: %v", err)
	}
}

func TestWrapCapabilityMatrix(t *testing.T) {
	f := NewFaults()

	wr := Wrap(stores.Stores{Runs: fakeRuns{}}, f, PortRuns).Runs
	if _, ok := wr.(stores.PreemptedLister); ok {
		t.Fatal("wrapped minimal runs must not satisfy PreemptedLister")
	}
	if _, ok := wr.(stores.CheckpointRuns); ok {
		t.Fatal("wrapped minimal runs must not satisfy CheckpointRuns")
	}

	wc := Wrap(stores.Stores{Checkpoints: fakeCheckpoints{}}, f, PortCheckpoints).Checkpoints
	if _, ok := wc.(stores.ResumeReadyLister); ok {
		t.Fatal("wrapped minimal checkpoints must not satisfy ResumeReadyLister")
	}
	if _, ok := wc.(stores.CheckpointResumer); ok {
		t.Fatal("wrapped minimal checkpoints must not satisfy CheckpointResumer")
	}

	wr = Wrap(stores.Stores{Runs: stores.NewMemoryRuns()}, f, PortRuns).Runs
	if _, ok := wr.(stores.PreemptedLister); !ok {
		t.Fatal("wrapped full runs must satisfy PreemptedLister")
	}
	if _, ok := wr.(stores.CheckpointRuns); !ok {
		t.Fatal("wrapped full runs must satisfy CheckpointRuns")
	}

	wc = Wrap(stores.Stores{Checkpoints: stores.NewMemoryCheckpoints()}, f, PortCheckpoints).Checkpoints
	if _, ok := wc.(stores.ResumeReadyLister); !ok {
		t.Fatal("wrapped full checkpoints must satisfy ResumeReadyLister")
	}
	if _, ok := wc.(stores.CheckpointResumer); !ok {
		t.Fatal("wrapped full checkpoints must satisfy CheckpointResumer")
	}
}

func TestMethodFailuresPerMethod(t *testing.T) {
	j := stores.NewMemoryJournal()
	f := NewFaults()
	w := Wrap(stores.Stores{Journal: j}, f, PortJournal).Journal

	f.FailNext(PortJournal, 1)
	key := types.CallKey{SessionID: "s", CallID: "c"}
	if _, _, err := w.Reserve(context.Background(), key, "tool", "fp", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Reserve under outage: want ErrUnavailable, got %v", err)
	}
	if got := f.MethodFailures(PortJournal, "Reserve"); got != 1 {
		t.Fatalf("MethodFailures(Reserve) = %d, want 1", got)
	}
	if got := f.MethodFailures(PortJournal, "Complete"); got != 0 {
		t.Fatalf("MethodFailures(Complete) = %d, want 0", got)
	}
	if got := f.MethodFailures(PortJournal, "ByFingerprint"); got != 0 {
		t.Fatalf("MethodFailures(ByFingerprint) = %d, want 0", got)
	}

	f.Clear(PortJournal)
	_, _, err := w.Reserve(context.Background(), key, "tool", "fp", nil)
	if err != nil {
		t.Fatalf("Reserve after Clear: %v", err)
	}
	if got := f.MethodFailures(PortJournal, "Reserve"); got != 0 {
		t.Fatalf("MethodFailures(Reserve) after Clear+success = %d, want 0", got)
	}
}

func TestOptionalSurfaceDelegation(t *testing.T) {
	ctx := context.Background()
	f := NewFaults()
	mr := stores.NewMemoryRuns()
	mc := stores.NewMemoryCheckpoints()
	st := Wrap(stores.Stores{Runs: mr, Checkpoints: mc}, f)
	wr := st.Runs
	wc := st.Checkpoints

	if _, ok := wr.(stores.CheckpointRuns); !ok {
		t.Fatal("wrapped runs does not satisfy stores.CheckpointRuns")
	}
	if _, ok := wc.(stores.CheckpointResumer); !ok {
		t.Fatal("wrapped checkpoints does not satisfy stores.CheckpointResumer")
	}
	if _, ok := wc.(stores.ResumeReadyLister); !ok {
		t.Fatal("wrapped checkpoints does not satisfy stores.ResumeReadyLister")
	}

	run := stores.Run{RunID: "r1", SessionID: "s1", State: stores.Running}
	lease, err := mr.Start(ctx, run, time.Minute)
	if err != nil {
		t.Fatalf("seed Start: %v", err)
	}
	token := types.ResumeToken("tok1")
	if err := mr.Suspend(ctx, lease, token); err != nil {
		t.Fatalf("seed Suspend: %v", err)
	}

	got, err := wr.(stores.CheckpointRuns).SuspensionToken(ctx, "r1")
	if err != nil || got != token {
		t.Fatalf("SuspensionToken delegation: token=%q err=%v", got, err)
	}
	resumeLease, err := wr.(stores.CheckpointRuns).ResumingCheckpoint(ctx, "r1", token, time.Minute)
	if err != nil || resumeLease.RunID != "r1" {
		t.Fatalf("ResumingCheckpoint delegation: lease=%+v err=%v", resumeLease, err)
	}

	cp := stores.Checkpoint{RunID: "r1", SessionID: "s1", Data: []byte("d")}
	cpToken, err := st.Checkpoints.Put(ctx, cp)
	if err != nil {
		t.Fatalf("seed Put: %v", err)
	}
	peeked, err := wc.(stores.CheckpointResumer).Peek(ctx, cpToken)
	if err != nil || peeked.RunID != "r1" {
		t.Fatalf("Peek delegation: cp=%+v err=%v", peeked, err)
	}
	consumed, err := wc.(stores.CheckpointResumer).ConsumeIf(ctx, cpToken, peeked, stores.ResumeInput{})
	if err != nil || consumed.RunID != "r1" {
		t.Fatalf("ConsumeIf delegation: cp=%+v err=%v", consumed, err)
	}
}

func TestOptionalSurfaceCountsOperations(t *testing.T) {
	ctx := context.Background()

	runF := NewFaults()
	runF.FailNext(PortRuns, 2)
	wr := Wrap(stores.Stores{Runs: stores.NewMemoryRuns()}, runF, PortRuns).Runs
	if _, err := wr.(stores.CheckpointRuns).SuspensionToken(ctx, "r"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SuspensionToken under outage: want ErrUnavailable, got %v", err)
	}
	if _, err := wr.(stores.PreemptedLister).Preempted(ctx, 10); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Preempted under outage: want ErrUnavailable, got %v", err)
	}
	if runF.Operations(PortRuns) != 2 || runF.Failures(PortRuns) != 2 {
		t.Fatalf("runs counters: ops=%d failures=%d, want 2/2", runF.Operations(PortRuns), runF.Failures(PortRuns))
	}

	cpF := NewFaults()
	cpF.FailNext(PortCheckpoints, 2)
	wc := Wrap(stores.Stores{Checkpoints: stores.NewMemoryCheckpoints()}, cpF, PortCheckpoints).Checkpoints
	resumer := wc.(stores.CheckpointResumer)
	if _, err := resumer.Peek(ctx, "t"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Peek under outage: want ErrUnavailable, got %v", err)
	}
	if _, err := wc.(stores.ResumeReadyLister).ResumeReady(ctx, 10); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ResumeReady under outage: want ErrUnavailable, got %v", err)
	}
	if cpF.Operations(PortCheckpoints) != 2 || cpF.Failures(PortCheckpoints) != 2 {
		t.Fatalf("checkpoint counters: ops=%d failures=%d, want 2/2", cpF.Operations(PortCheckpoints), cpF.Failures(PortCheckpoints))
	}
}

func TestNilPortsStayNil(t *testing.T) {
	st := stores.Stores{Runs: stores.NewMemoryRuns()}
	w := Wrap(st, NewFaults())
	if w.Journal != nil || w.SessionLog != nil || w.EventLog != nil || w.Checkpoints != nil {
		t.Fatal("absent ports must stay nil after Wrap")
	}
}

// fakeRuns and fakeCheckpoints implement the frozen port interfaces without
// any optional surface, exercising the minimal capability variants.

type fakeRuns struct{ stores.Runs }

func (fakeRuns) Start(context.Context, stores.Run, time.Duration) (stores.Lease, error) {
	return stores.Lease{}, nil
}
func (fakeRuns) Heartbeat(context.Context, stores.Lease) (stores.Lease, error) {
	return stores.Lease{}, nil
}
func (fakeRuns) Suspend(context.Context, stores.Lease, types.ResumeToken) error { return nil }
func (fakeRuns) Resuming(context.Context, string, time.Duration) (stores.Lease, error) {
	return stores.Lease{}, nil
}
func (fakeRuns) Finish(context.Context, stores.Lease, stores.RunState, []types.CallKey, string) error {
	return nil
}
func (fakeRuns) ByOperation(context.Context, string, string) (stores.Run, error) {
	return stores.Run{}, nil
}
func (fakeRuns) Stale(context.Context, time.Duration, int) ([]stores.Run, error) { return nil, nil }
func (fakeRuns) Reclaim(context.Context, stores.Run, time.Duration) (stores.Lease, error) {
	return stores.Lease{}, nil
}
func (fakeRuns) Signal(context.Context, string, stores.Signal) error          { return nil }
func (fakeRuns) Drain(context.Context, stores.Lease) ([]stores.Signal, error) { return nil, nil }
func (fakeRuns) Notices(context.Context, int) ([]types.RunNotice, error)      { return nil, nil }
func (fakeRuns) AckNotice(context.Context, string) error                      { return nil }

type fakeCheckpoints struct{ stores.Checkpoints }

func (fakeCheckpoints) Put(context.Context, stores.Checkpoint) (types.ResumeToken, error) {
	return "", nil
}
func (fakeCheckpoints) Consume(context.Context, types.ResumeToken, stores.ResumeInput) (stores.Checkpoint, error) {
	return stores.Checkpoint{}, nil
}
func (fakeCheckpoints) PendingInput(context.Context, string) (stores.Checkpoint, stores.ResumeInput, error) {
	return stores.Checkpoint{}, stores.ResumeInput{}, nil
}
