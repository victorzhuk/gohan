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

// planRT is a minimal foreign runtime that declares the optional resume
// identity: the flow kind its checkpoints resume as and the backend
// version it writes. Steps finish at once, so a replay drive completes
// without a backend.
type planRT struct {
	name    string
	kind    stores.FlowKind
	version string
}

func (r *planRT) Name() string                         { return r.name }
func (r *planRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }
func (r *planRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}
func (r *planRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	return st, []types.Event{types.Done{}}, runtime.DoneStatus, nil
}
func (r *planRT) FlowKind() stores.FlowKind { return r.kind }
func (r *planRT) BackendVersion() string    { return r.version }

// seedPlanHarness builds a conversation over rt whose flow "chat" holds
// one suspended run with a token for a checkpoint written under the
// stored version. The telemetry sink records the counters the resume
// path emits.
func seedPlanHarness(t *testing.T, rt *planRT, storedVersion string) (Conversation, types.ResumeToken, *planTel) {
	t.Helper()
	ctx := context.Background()
	alice := types.Principal{Subject: "alice", Tenant: "t1", Scopes: []string{"flow:run"}}
	sink := newPlanTel()
	stack, err := Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	stack.telemetry = sink
	stack.stores = stores.Stores{
		SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
	}
	runs := stores.NewMemoryRuns()
	cps := stores.NewMemoryCheckpoints()
	conv, err := NewConversation(stack, "chat", rt,
		WithConversationRuns(runs),
		WithConversationEventLog(stores.NewMemoryEventLog()),
		WithConversationCheckpoints(cps),
	)
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}

	ownCtx := types.WithPrincipal(ctx, alice)
	const sessionID = "s1"
	if _, err := stack.stores.SessionLog.Append(ownCtx, sessionID, 0, types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: "hi"}},
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	lease, err := runs.Start(ownCtx, stores.Run{
		RunID:       "run-1",
		Flow:        "chat",
		SessionID:   sessionID,
		OperationID: "op-1",
	}, stores.LeaseTTL)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	data, err := encodeCheckpoint(checkpointEnvelope{
		Version:    checkpointEnvelopeVersion,
		Run:        types.RunInfo{RunID: "run-1", SessionID: sessionID, Flow: "chat"},
		Generation: 1,
		State:      runtime.State{Turn: 1, HistoryVersion: 1},
	})
	if err != nil {
		t.Fatalf("encode checkpoint: %v", err)
	}
	tok, err := cps.Put(ownCtx, stores.Checkpoint{
		RunID:          "run-1",
		SessionID:      sessionID,
		Flow:           "chat",
		SchemaVersion:  stores.CurrentSchemaVersion,
		Backend:        rt.name,
		BackendVersion: storedVersion,
		Reason:         types.Preempted,
		Originator:     alice,
		Data:           data,
		ExpiresAt:      time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	// The run must wait on the exact token the checkpoint minted: the
	// token-fenced lease refuses a suspension parked on another token.
	if err := runs.Suspend(ownCtx, lease, tok); err != nil {
		t.Fatalf("seed suspend: %v", err)
	}
	return conv, tok, sink
}

// planTel records the counters the resume decision emits.
type planTel struct {
	counts map[string]int64
}

func newPlanTel() *planTel { return &planTel{counts: map[string]int64{}} }

func (t *planTel) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}
func (t *planTel) Count(_ context.Context, name string, n int64, _ ...types.Attr) {
	t.counts[name] += n
}
func (*planTel) Record(context.Context, string, float64, ...types.Attr) {}

func TestResumePlanWiring(t *testing.T) {
	ctx := context.Background()
	alice := types.Principal{Subject: "alice", Tenant: "t1"}

	t.Run("stores.graph-checkpoint-incompatible", func(t *testing.T) {
		rt := &planRT{name: "eino", kind: stores.FlowGraph, version: "adapter-y"}
		conv, tok, _ := seedPlanHarness(t, rt, "adapter-x")
		var got error
		for _, err := range conv.Resume(types.WithPrincipal(ctx, alice), tok, stores.ResumeInput{}) {
			if err != nil {
				got = err
			}
		}
		if !errors.Is(got, types.ErrCheckpointIncompatible) {
			t.Fatalf("resume err = %v, want ErrCheckpointIncompatible", got)
		}
	})

	t.Run("stores.native-checkpoint-after-adapter-upgrade", func(t *testing.T) {
		rt := &planRT{name: "eino", kind: stores.FlowAgent, version: "adapter-y"}
		conv, tok, sink := seedPlanHarness(t, rt, "adapter-x")
		var done bool
		for ev, err := range conv.Resume(types.WithPrincipal(ctx, alice), tok, stores.ResumeInput{}) {
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			if _, ok := ev.(types.Done); ok {
				done = true
			}
		}
		if !done {
			t.Fatal("replayed resume produced no Done")
		}
		if n := sink.counts["gohan.resume.fallback_replay"]; n != 1 {
			t.Fatalf("gohan.resume.fallback_replay = %d, want 1", n)
		}
	})

	t.Run("same_version_resumes_natively", func(t *testing.T) {
		rt := &planRT{name: "eino", kind: stores.FlowGraph, version: "adapter-x"}
		conv, tok, sink := seedPlanHarness(t, rt, "adapter-x")
		var done bool
		for ev, err := range conv.Resume(types.WithPrincipal(ctx, alice), tok, stores.ResumeInput{}) {
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			if _, ok := ev.(types.Done); ok {
				done = true
			}
		}
		if !done {
			t.Fatal("native resume produced no Done")
		}
		if n := sink.counts["gohan.resume.fallback_replay"]; n != 0 {
			t.Fatalf("gohan.resume.fallback_replay = %d, want 0", n)
		}
	})
}
