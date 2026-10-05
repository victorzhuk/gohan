package gohan

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"iter"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// mixedModel scripts one model turn that emits several tool calls in a
// single response, so a batch carries both a settled call and an ask.
type mixedModel struct {
	calls []types.ToolUse
}

func (m *mixedModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}
}

func (m *mixedModel) Generate(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		for i := range m.calls {
			yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &m.calls[i]}, nil)
		}
		yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
	}
}

func TestNativePhasePreservesDriverRecord(t *testing.T) {
	ctx := context.Background()
	const driver = `{"version":1,"tool_calls":3}`
	n := runtime.NewNative()
	st, err := n.Start(ctx, runtime.AgentRun{
		ModelEffect: func(_ context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
			return st, nil, runtime.Continue, nil
		},
		BatchEffect: func(_ context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
			st.Backend = json.RawMessage(`{"phase":"batch","driver":` + driver + `}`)
			return st, nil, runtime.Continue, nil
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	st, _, status, err := n.Step(ctx, st)
	if err != nil || status != runtime.Continue {
		t.Fatalf("model step: status %v err %v", status, err)
	}
	var p nativePhaseAlias
	if err := json.Unmarshal(st.Backend, &p); err != nil {
		t.Fatalf("decode backend: %v", err)
	}
	if p.Phase != "batch" || len(p.Driver) != 0 {
		t.Fatalf("after model step = %s, want batch phase without driver", st.Backend)
	}

	st, _, _, err = n.Step(ctx, st)
	if err != nil {
		t.Fatalf("batch step: %v", err)
	}
	if err := json.Unmarshal(st.Backend, &p); err != nil {
		t.Fatalf("decode backend: %v", err)
	}
	if p.Phase != "model" || string(p.Driver) != driver {
		t.Fatalf("after batch step = %s, want model phase with driver %s", st.Backend, driver)
	}

	st, _, _, err = n.Step(ctx, st)
	if err != nil {
		t.Fatalf("second model step: %v", err)
	}
	if err := json.Unmarshal(st.Backend, &p); err != nil {
		t.Fatalf("decode backend: %v", err)
	}
	if p.Phase != "batch" || string(p.Driver) != driver {
		t.Fatalf("after second model step = %s, want batch phase with driver %s", st.Backend, driver)
	}
}

// nativePhaseAlias mirrors the private runtime phase record without
// reaching into the runtime package.
type nativePhaseAlias struct {
	Phase  string          `json:"phase"`
	Driver json.RawMessage `json:"driver,omitempty"`
}

// TestNativeBatchPersistsContinuation drives a mixed batch through the
// public native path: the settled read-only result is already in the
// session log at the suspension, and the persisted state carries a driver
// record whose calls cover the whole gated batch with the asked call
// unresolved.
func TestNativeBatchPersistsContinuation(t *testing.T) {
	model := &mixedModel{calls: []types.ToolUse{nrrNote("n1"), nrrBook("c2")}}
	book := &bookTool{}
	note := &nrrNoteTool{}
	stack, conv := nrrStack(t, model, book, note, nil, nil)
	token := suspendToken(t, conv)

	h, err := stack.stores.SessionLog.Load(nrrCtx(), "s1")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	found := false
	for _, msg := range h.Messages {
		for _, b := range msg.Blocks {
			if res, ok := b.(types.ToolResult); ok && res.ID == "n1" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("settled note result missing from session log at suspension: %d messages", len(h.Messages))
	}

	cps, ok := stack.stores.Checkpoints.(*stores.MemoryCheckpoints)
	if !ok {
		t.Fatalf("checkpoints store type %T", stack.stores.Checkpoints)
	}
	cp, err := cps.Peek(nrrCtx(), token)
	if err != nil {
		t.Fatalf("peek checkpoint: %v", err)
	}
	_, st, err := decodeCheckpoint(cp, runtime.NewNative(), "chat")
	if err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}
	rec, err := decodeNativeBackend(st)
	if err != nil {
		t.Fatalf("decode backend record: %v", err)
	}
	if rec.Driver == nil || rec.Driver.Batch == nil {
		t.Fatalf("no driver batch record in %s", st.Backend)
	}
	d := rec.Driver
	if d.Version != nativeProgressVersion || d.ToolCalls != 2 {
		t.Fatalf("driver = %+v, want version %d with two admitted calls", d, nativeProgressVersion)
	}
	calls := d.Batch.Calls
	if len(calls) != 2 || calls[0].ID != "n1" || calls[1].ID != "c2" {
		t.Fatalf("batch calls = %+v, want [n1 c2] in order", calls)
	}
	results := d.Batch.Results
	if len(results) != 2 || results[0].ID != "n1" || results[1].ID != "" {
		t.Fatalf("batch results = %+v, want settled n1 and unresolved c2", results)
	}
	if d.Batch.Ready {
		t.Fatal("driver record ready at an ask suspension")
	}
	if book.ran() != nil {
		t.Fatalf("asked side effect executed: %v", book.ran())
	}
}

func TestCheckpointEnvelopeVersionTwoRoundTrip(t *testing.T) {
	env := checkpointEnvelope{
		Run:        types.RunInfo{RunID: "r1", SessionID: "s1", Flow: "chat"},
		Generation: 1,
		State:      runtime.State{Backend: json.RawMessage(`{"phase":"batch"}`)},
	}
	data, err := encodeCheckpoint(env)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	cp := stores.Checkpoint{RunID: "r1", SessionID: "s1", Flow: "chat", Backend: "native", Reason: types.AwaitingBatch, Data: data}
	got, st, err := decodeCheckpoint(cp, runtime.NewNative(), "chat")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Version != checkpointEnvelopeVersion || got.Generation != 1 {
		t.Fatalf("decoded envelope = %+v", got)
	}
	if string(st.Backend) != `{"phase":"batch"}` {
		t.Fatalf("decoded state backend = %s", st.Backend)
	}
}

// TestCheckpointEnvelopeV1NativeApprovalRefused hand-crafts the version 1
// bytes: a native approval checkpoint carries no reservation or
// settled-result evidence, so it refuses before any consumption.
func TestCheckpointEnvelopeV1NativeApprovalRefused(t *testing.T) {
	call := types.ToolUse{ID: "c1", Name: "book", Args: jsontext.Value(`{"id":"x"}`)}
	data, err := json.Marshal(checkpointEnvelope{
		Version:    1,
		Run:        types.RunInfo{RunID: "r1", SessionID: "s1", Flow: "chat"},
		Generation: 1,
		State:      runtime.State{Pending: []types.ToolUse{call}},
		Approvals:  []checkpointApproval{{Call: call}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	cp := stores.Checkpoint{RunID: "r1", SessionID: "s1", Flow: "chat", Backend: "native", Reason: types.HumanApproval, Data: data}
	_, _, err = decodeCheckpoint(cp, runtime.NewNative(), "chat")
	if !errors.Is(err, types.ErrCheckpointIncompatible) {
		t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
	}
}

// TestCheckpointEnvelopeV1MultiApprovalRefused refuses a version 1
// envelope that carries more than one approval, even for a foreign
// backend whose single-approval shape would otherwise stay resumable.
func TestCheckpointEnvelopeV1MultiApprovalRefused(t *testing.T) {
	rt := &roundtripRT{}
	call := types.ToolUse{ID: "c1", Name: "confirm", Args: jsontext.Value(`{"n":1}`)}
	other := types.ToolUse{ID: "c2", Name: "confirm", Args: jsontext.Value(`{"n":2}`)}
	data, err := json.Marshal(checkpointEnvelope{
		Version:    1,
		Run:        types.RunInfo{RunID: "r1", SessionID: "s1", Flow: "flights"},
		Generation: 1,
		State:      runtime.State{Pending: []types.ToolUse{call, other}},
		Approvals:  []checkpointApproval{{Call: call}, {Call: other}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	cp := stores.Checkpoint{RunID: "r1", SessionID: "s1", Flow: "flights", Backend: rt.Name(), Reason: types.HumanApproval, Data: data}
	_, _, err = decodeCheckpoint(cp, rt, "flights")
	if !errors.Is(err, types.ErrCheckpointIncompatible) {
		t.Fatalf("err = %v, want ErrCheckpointIncompatible", err)
	}
}

func TestApprovalReceiptVersionIndependent(t *testing.T) {
	if approvalReceiptVersion == checkpointEnvelopeVersion {
		t.Fatal("receipt version tracks the envelope version; it must stay independent")
	}
	msg, err := approvalReceiptMessage(approvalReceipt{
		RunID:  "r1",
		CallID: "c1",
		Tool:   "book",
		Args:   jsontext.Value(`{"id":"x"}`),
		Approvers: []types.Principal{
			{Subject: "op", Tenant: "t1"},
		},
	})
	if err != nil {
		t.Fatalf("encode receipt: %v", err)
	}
	rc, err := decodeReceipt(msg.Meta[ApprovalReceiptKey])
	if err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if rc.Version != approvalReceiptVersion {
		t.Fatalf("receipt version = %d, want %d", rc.Version, approvalReceiptVersion)
	}
}
