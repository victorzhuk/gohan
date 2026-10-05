package gohan

import (
	"context"
	"encoding/json/jsontext"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// identityObs records one governed tool invocation with the run identity
// the drive context carried into it.
type identityObs struct {
	callID  string
	session string
	runID   string
	flow    string
}

// identityRecorder is a tool-chain step that observes the (CallID,
// RunInfo) pair each governed tool call executes under.
type identityRecorder struct {
	mu  sync.Mutex
	obs []identityObs
}

func (r *identityRecorder) step(next chains.ToolFunc) chains.ToolFunc {
	return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
		info, _ := types.RunInfoFrom(ctx)
		r.mu.Lock()
		r.obs = append(r.obs, identityObs{callID: call.ID, session: info.SessionID, runID: info.RunID, flow: info.Flow})
		r.mu.Unlock()
		return next(ctx, call)
	}
}

func (r *identityRecorder) seen() []identityObs {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]identityObs(nil), r.obs...)
}

// noteThenDone scripts one turn: emit the note call, then after its
// result is in the history, finish the run.
func noteThenDone(id string) types.ModelFunc {
	return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			for _, m := range req.Messages {
				for _, b := range m.Blocks {
					if _, ok := b.(types.ToolResult); ok {
						yield(types.ModelChunk{Kind: types.DeltaText, Delta: "done"}, nil)
						yield(types.ModelChunk{Finish: types.FinishStop}, nil)
						return
					}
				}
			}
			yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: id, Name: "note", Args: jsontext.Value(`{` + `}`)}}, nil)
			yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
		}
	}
}

// identityStack builds a native conversation over one scoped read-only
// note tool and the recorder, so a run's identity is observable at the
// governed tool boundary.
func identityStack(t *testing.T, rec *identityRecorder) (*Stack, Conversation, *stores.MemoryRuns) {
	t.Helper()
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	stack, err := Build(
		WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        runs,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		}),
		WithModels(&funcModel{fn: noteThenDone("c-shared")}),
		WithLimits("chat", nativeConvLimits(8)),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "native",
			Tools:   []types.Tool{&nrrNoteTool{}},
			ToolChain: chains.ToolChain{{
				Name: "identity-probe", Kind: chains.KindUser,
				Use: rec.step,
			}},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := NewNativeConversation(stack, "chat",
		WithConversationRuns(runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		WithConversationApprovalPolicy(nrrAllowPolicy{}),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return stack, conv, runs
}

var identityOwner = types.Principal{Subject: "u1", Tenant: "t1", Scopes: []string{"note.write"}}

func identityCtx() context.Context {
	return types.WithPrincipal(context.Background(), identityOwner)
}

func identityDrain(t *testing.T, seq iter.Seq2[Event, error]) {
	t.Helper()
	for _, err := range seq {
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	}
}

func identitySingle(t *testing.T, rec *identityRecorder) identityObs {
	t.Helper()
	obs := rec.seen()
	if len(obs) != 1 {
		t.Fatalf("tool calls observed = %d, want 1 (%v)", len(obs), obs)
	}
	return obs[0]
}

// A Send driven with only a principal executes a scope-guarded tool: the
// run identity the driver acquired reaches the gate and the tool, so the
// scope check succeeds and the observation carries the session, flow and
// acquired run.
func TestNativeSendInstallsRunIdentity(t *testing.T) {
	rec := &identityRecorder{}
	_, conv, runs := identityStack(t, rec)
	identityDrain(t, conv.Send(identityCtx(), "s1", userMsg("hi")))
	obs := identitySingle(t, rec)
	if obs.session != "s1" || obs.flow != "chat" || obs.runID == "" {
		t.Fatalf("run identity at tool = %+v, want session s1, flow chat, non-empty run", obs)
	}
	recRun, err := runs.ByID(context.Background(), obs.runID)
	if err != nil {
		t.Fatalf("acquired run %q not in runs store: %v", obs.runID, err)
	}
	if recRun.SessionID != "s1" || recRun.Flow != "chat" {
		t.Fatalf("acquired run record = %+v, want session s1 flow chat", recRun)
	}
}

// A Continue installs the identity of the run it acquires the same way,
// before it loads history or drives the stream.
func TestNativeContinueInstallsRunIdentity(t *testing.T) {
	rec := &identityRecorder{}
	stack, conv, runs := identityStack(t, rec)
	if _, err := stack.stores.SessionLog.Append(identityCtx(), "s2", 0, userMsg("seed")); err != nil {
		t.Fatalf("seed history: %v", err)
	}
	identityDrain(t, conv.Continue(identityCtx(), "s2"))
	obs := identitySingle(t, rec)
	if obs.session != "s2" || obs.flow != "chat" || obs.runID == "" {
		t.Fatalf("run identity at tool = %+v, want session s2, flow chat, non-empty run", obs)
	}
	recRun, err := runs.ByID(context.Background(), obs.runID)
	if err != nil {
		t.Fatalf("acquired run %q not in runs store: %v", obs.runID, err)
	}
	if recRun.SessionID != "s2" || recRun.Flow != "chat" {
		t.Fatalf("acquired run record = %+v, want session s2 flow chat", recRun)
	}
}

// The same tool call executed in two sessions is observed under two
// distinct session identities, so the journal key inputs differ per
// session and cannot collide on CallID alone.
func TestNativeJournalIsolatesSessions(t *testing.T) {
	rec := &identityRecorder{}
	_, conv, _ := identityStack(t, rec)
	identityDrain(t, conv.Send(identityCtx(), "sa", userMsg("hi")))
	identityDrain(t, conv.Send(identityCtx(), "sb", userMsg("hi")))
	obs := rec.seen()
	if len(obs) != 2 {
		t.Fatalf("tool calls observed = %d, want 2", len(obs))
	}
	if obs[0].callID != "c-shared" || obs[1].callID != "c-shared" {
		t.Fatalf("call ids = %q, %q, want the same call in both sessions", obs[0].callID, obs[1].callID)
	}
	if obs[0].session == obs[1].session {
		t.Fatalf("both sessions observed SessionID %q, want distinct identities", obs[0].session)
	}
	sessions := map[string]bool{obs[0].session: true, obs[1].session: true}
	if !sessions["sa"] || !sessions["sb"] {
		t.Fatalf("observed sessions = %v, want sa and sb", sessions)
	}
	if obs[0].runID == obs[1].runID {
		t.Fatalf("both sessions observed RunID %q, want one per acquired run", obs[0].runID)
	}
}

// A caller-supplied ambient RunInfo does not reach the run: the drive
// observes the acquired identity, not the seeded one.
func TestNativeAmbientRunInfoDoesNotOverrideAcquiredRun(t *testing.T) {
	rec := &identityRecorder{}
	_, conv, runs := identityStack(t, rec)
	ambient := types.WithRunInfo(identityCtx(), types.RunInfo{Flow: "ambient", SessionID: "ambient-session", RunID: "ambient-run", RootRunID: "ambient-run"})
	identityDrain(t, conv.Send(ambient, "s1", userMsg("hi")))
	obs := identitySingle(t, rec)
	if obs.session == "ambient-session" || obs.runID == "ambient-run" || obs.flow == "ambient" {
		t.Fatalf("ambient RunInfo survived into the run: %+v", obs)
	}
	if obs.session != "s1" || obs.flow != "chat" || obs.runID == "" {
		t.Fatalf("run identity at tool = %+v, want session s1, flow chat, non-empty run", obs)
	}
	if _, err := runs.ByID(context.Background(), obs.runID); err != nil {
		t.Fatalf("observed run %q is not the acquired run: %v", obs.runID, err)
	}
}
