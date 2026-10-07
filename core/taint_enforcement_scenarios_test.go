package gohan_test

import (
	"context"
	"strings"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/taint"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

type taintEnforceMetric struct {
	name  string
	value int64
	attrs []types.Attr
}

type taintEnforceTelemetry struct {
	metrics []taintEnforceMetric
}

func (m *taintEnforceTelemetry) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (m *taintEnforceTelemetry) Count(_ context.Context, name string, value int64, attrs ...types.Attr) {
	m.metrics = append(m.metrics, taintEnforceMetric{name: name, value: value, attrs: append([]types.Attr(nil), attrs...)})
}

func (m *taintEnforceTelemetry) Record(_ context.Context, _ string, _ float64, _ ...types.Attr) {}

type taintEnforceApprovalPolicy struct{}

func (taintEnforceApprovalPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{}, nil
}

func TestTaintEnforcementScenarios(t *testing.T) {
	const secret = "sensitive phrase from retrieved page"
	for _, scenario := range []struct {
		name      string
		effect    types.Effect
		exfil     bool
		liveDeny  bool
		wantCalls int
	}{
		{name: "taint.exfil-denied", exfil: true},
		{name: "taint.read-only-allowed", effect: types.ReadOnly, wantCalls: 1},
		{name: "taint.side-effect-asks", effect: types.SideEffect},
		{name: "taint.gate-order", effect: types.SideEffect, liveDeny: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			telemetry := &taintEnforceTelemetry{}
			hook, err := taint.New(taint.Default(), telemetry)
			if err != nil {
				t.Fatal(err)
			}
			fetch := gohantest.NewFakeTool("fetch", gohantest.WithToolResult(types.ToolResult{Outcome: types.Succeeded, Content: []types.Block{types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "fetch"}}, Text: secret}}}))
			target := gohantest.NewFakeTool("target", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
				spec.Effect = scenario.effect
				spec.Capabilities.Exfil = scenario.exfil
			}))
			model := gohantest.NewScriptedModel(types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}},
				gohantest.ToolCall("fetch", map[string]any{}),
				gohantest.ToolCall("target", map[string]any{"value": secret}),
				gohantest.Text("finished"),
			)
			runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				principal, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: principal}, ok
			}))
			native := gohan.NativeSpec{
				Request:     gohan.FlowRequest{Name: "chat"},
				Profile:     "native",
				Tools:       []types.Tool{fetch, target},
				TaintPolicy: hook,
				Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
				},
			}
			if scenario.liveDeny {
				native.LiveControl = func(context.Context, *permission.ToolInvocation) (types.TaintAction, string) {
					return types.TaintDeny, "emergency deny"
				}
			}
			stack, err := gohan.Build(
				gohan.AllowAnonymous(),
				gohan.WithTelemetry(telemetry),
				gohan.WithStores(stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)), Runs: runs, Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))}),
				gohan.WithModels(model),
				gohan.WithNativeAgent(native),
			)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			conv, err := gohan.NewNativeConversation(stack, "chat",
				gohan.WithConversationRuns(runs),
				gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
				gohan.WithConversationApprovalPolicy(taintEnforceApprovalPolicy{}),
			)
			if err != nil {
				t.Fatalf("NewNativeConversation: %v", err)
			}
			ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "tenant", Subject: "user"})
			var susp types.Suspended
			for ev, sendErr := range conv.Send(ctx, "session", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "fetch and use " + secret}}}) {
				if sendErr != nil {
					t.Fatalf("Send: %v", sendErr)
				}
				if event, ok := ev.(types.Suspended); ok {
					susp = event
				}
			}
			if got := len(target.Calls()); got != scenario.wantCalls {
				t.Fatalf("target executions = %d, want %d", got, scenario.wantCalls)
			}
			if scenario.exfil {
				if len(model.Requests()) < 3 {
					t.Fatalf("model requests = %d, want refusal result", len(model.Requests()))
				}
				foundDenied := false
				for _, message := range model.Requests()[2].Messages {
					for _, block := range message.Blocks {
						result, ok := block.(types.ToolResult)
						if ok && result.Error != nil && strings.Contains(result.Error.Message, "value") && strings.Contains(result.Error.Message, "untrusted content") {
							foundDenied = true
						}
					}
				}
				if !foundDenied {
					t.Fatal("model-visible tool result did not carry TaintDenied for argument value")
				}
				foundMetric := false
				for _, metric := range telemetry.metrics {
					if metric.name != "gohan.taint.denied" || metric.value < 1 {
						continue
					}
					for _, attr := range metric.attrs {
						if attr.Key == "tool" && attr.Value == "target" {
							foundMetric = true
						}
					}
				}
				if !foundMetric {
					t.Fatal("gohan.taint.denied metric did not count target")
				}
			}
			if scenario.effect == types.SideEffect && !scenario.liveDeny {
				if susp.Token == "" || susp.Reason != types.HumanApproval {
					t.Fatalf("suspension = %+v, want HumanApproval", susp)
				}
				req, ok := susp.Payload.(permission.ApprovalRequest)
				if !ok {
					t.Fatalf("suspension payload = %T, want permission.ApprovalRequest", susp.Payload)
				}
				if origin, ok := req.ArgOrigins["value"]; !ok || origin.Kind != types.OriginTool || origin.Name != "fetch" {
					t.Fatalf("ArgOrigins[value] = %+v, want fetch tool origin", req.ArgOrigins["value"])
				}
			}
			if scenario.liveDeny {
				if susp.Token != "" {
					t.Fatalf("suspension = %+v, want no suspension", susp)
				}
				foundPermanent := false
				for _, request := range model.Requests() {
					for _, message := range request.Messages {
						for _, block := range message.Blocks {
							result, ok := block.(types.ToolResult)
							if ok && result.Error != nil && result.Error.Kind == types.Permanent && strings.Contains(result.Error.Message, "emergency deny") {
								foundPermanent = true
							}
						}
					}
				}
				if !foundPermanent {
					t.Fatal("model-visible result did not carry permanent live denial")
				}
			}
		})
	}
}
