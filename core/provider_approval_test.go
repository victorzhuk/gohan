package gohan

import (
	"context"
	"encoding/json/jsontext"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

func TestProviderApproval(t *testing.T) {
	mcp := types.ToolSpec{Name: "hosted_mcp", Executor: types.ByProvider, Effect: types.SideEffect}
	call := types.ToolUse{ID: "call-1", Name: "hosted_mcp", Args: jsontext.Value(`{"url":"https://mcp.example"}`)}
	taints := []types.ArgTaint{{
		Arg:     "url",
		Origins: []types.Origin{{Kind: types.OriginTool, Name: "read_pdf"}},
	}}

	t.Run("tools.provider-approval-as-suspension", func(t *testing.T) {
		var st runtime.State
		susp, err := providerApprovalPending(&st, call, taints)
		if err != nil {
			t.Fatalf("providerApprovalPending: %v", err)
		}
		if susp.Reason != types.HumanApproval {
			t.Fatalf("reason = %v, want HumanApproval", susp.Reason)
		}
		if len(st.Pending) != 1 || st.Pending[0].Name != "hosted_mcp" || st.Pending[0].ID != call.ID {
			t.Fatalf("pending = %+v, want the provider call %v", st.Pending, call)
		}
		b, err := decodeNativeBackend(st)
		if err != nil {
			t.Fatalf("decode backend: %v", err)
		}
		if b.Driver == nil || b.Driver.Batch == nil || len(b.Driver.Batch.Taints) != 1 || b.Driver.Batch.Taints[0].Arg != "url" {
			t.Fatalf("batch taints lost: %+v", b.Driver)
		}
	})

	t.Run("payload carries arg origins from the pre-call taints", func(t *testing.T) {
		inv := &permission.ToolInvocation{Spec: mcp, Call: call, Taints: taints}
		req := permission.ProviderApproval(inv, nil, time.Unix(1000, 0))
		origin, ok := req.ArgOrigins["url"]
		if !ok || origin.Kind != types.OriginTool || origin.Name != "read_pdf" {
			t.Fatalf("ArgOrigins[url] = %+v, want read_pdf tool origin", origin)
		}
		if req.Call.ID != call.ID || req.Tool.Name != mcp.Name {
			t.Fatalf("payload names %s/%s, want the pending call", req.Tool.Name, req.Call.ID)
		}
	})

	t.Run("approved ask replays the model call without executing", func(t *testing.T) {
		b := nativeBackend{Phase: "batch", Driver: &nativeProgress{
			Version:   nativeProgressVersion,
			ToolCalls: 1,
			Batch:     &nativeBatchContinuation{Calls: []types.ToolUse{call}},
		}}
		enc, err := encodeNativeBackend(b)
		if err != nil {
			t.Fatalf("encode backend: %v", err)
		}
		st := runtime.State{Backend: enc, Pending: []types.ToolUse{call}}
		env := &turnEnv{c: turnConfig{specs: []types.ToolSpec{mcp}}}
		st2, _, status, err := replayAskEffect(withTurnEnv(context.Background(), env), st)
		if err != nil {
			t.Fatalf("replayAskEffect: %v", err)
		}
		if status != runtime.Continue {
			t.Fatalf("status = %v, want Continue into the model replay", status)
		}
		if len(st2.Pending) != 0 {
			t.Fatalf("pending = %+v, want the ask cleared for replay", st2.Pending)
		}
		b2, err := decodeNativeBackend(st2)
		if err != nil {
			t.Fatalf("decode backend: %v", err)
		}
		if b2.Driver == nil || b2.Driver.Batch == nil || len(b2.Driver.Batch.Calls) != 0 {
			t.Fatalf("batch calls = %+v, want the settled ask dropped", b2.Driver)
		}
	})

	t.Run("harness ask still executes through the gate", func(t *testing.T) {
		local := types.ToolUse{ID: "call-2", Name: "book"}
		b := nativeBackend{Phase: "batch", Driver: &nativeProgress{
			Version:   nativeProgressVersion,
			ToolCalls: 1,
			Batch:     &nativeBatchContinuation{Calls: []types.ToolUse{local}},
		}}
		enc, err := encodeNativeBackend(b)
		if err != nil {
			t.Fatalf("encode backend: %v", err)
		}
		st := runtime.State{Backend: enc, Pending: []types.ToolUse{local}}
		env := &turnEnv{c: turnConfig{specs: []types.ToolSpec{{Name: "book", Effect: types.SideEffect}}}}
		_, _, status, err := replayAskEffect(withTurnEnv(context.Background(), env), st)
		if err == nil {
			t.Fatalf("err = nil, want the unapproved harness ask to re-suspend")
		}
		if status != runtime.SuspendedStatus {
			t.Fatalf("status = %v, want SuspendedStatus", status)
		}
	})
}
