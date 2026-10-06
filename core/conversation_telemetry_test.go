package gohan

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

func TestTelemetryConversationRunSpan(t *testing.T) {
	tel := &fakeTelemetry{}
	native := NativeSpec{
		Request: FlowRequest{Name: "chat"},
		Profile: "primary",
		Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
			return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
		},
	}
	var logBuf bytes.Buffer
	stack, err := Build(
		AllowAnonymous(),
		WithTelemetry(tel),
		WithLogger(captureLogger(&logBuf)),
		WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom)),
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		}),
		WithModels(gohantest.NewScriptedModel(types.ModelProfile{Name: "primary"}, gohantest.Text("acknowledged"))),
		WithNativeAgent(native),
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	conv, err := NewNativeConversation(stack, "chat",
		WithConversationRuns(stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatalf("NewNativeConversation: %v", err)
	}
	ctx := types.WithTelemetry(types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"}), tel)
	for _, sendErr := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}) {
		if sendErr != nil {
			t.Fatalf("Send: %v", sendErr)
		}
	}
	run := tel.span(t, SpanRun)
	if !run.ended {
		t.Fatal("run span never ended")
	}
	if tel.attr(t, run, types.KeySessionID).Value != "s1" {
		t.Fatalf("run span session: %v", tel.attr(t, run, types.KeySessionID))
	}
}
