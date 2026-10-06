package gohan_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/flow"
	"github.com/victorzhuk/gohan/std/taint"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

type Invoice struct {
	IBAN string `json:"iban"`
	Name string `json:"name"`
}

func TestQuarantine(t *testing.T) {
	const iban = "DE44500105175407324931"
	const pdfText = "Invoice from Nordwind GmbH, pay to IBAN " + iban
	pdfOrigin := types.Origin{Kind: types.OriginTool, Name: "read_pdf"}
	value := Invoice{IBAN: iban, Name: "Nordwind GmbH"}

	t.Run("record-one-block-per-origin", func(t *testing.T) {
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		stack, err := gohan.Build(
			gohan.AllowAnonymous(),
			gohan.WithStores(stores.Stores{SessionLog: log}),
		)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "tenant", Subject: "user"})
		if _, err := log.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		q := types.Quarantined[Invoice]{
			Value:   value,
			Origins: []types.Origin{pdfOrigin, {Kind: types.OriginProvider, Name: "mail"}, pdfOrigin},
		}
		if err := gohan.RecordQuarantined(ctx, stack, "s1", q); err != nil {
			t.Fatalf("RecordQuarantined: %v", err)
		}
		hist, err := log.Load(ctx, "s1")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		recorded := hist.Messages[len(hist.Messages)-1]
		if got := len(recorded.Blocks); got != 2 {
			t.Fatalf("recorded blocks = %d, want one per distinct origin", got)
		}
		for i, want := range []types.Origin{pdfOrigin, {Kind: types.OriginProvider, Name: "mail"}} {
			result, ok := recorded.Blocks[i].(types.ToolResult)
			if !ok {
				t.Fatalf("block %d is %T, want ToolResult", i, recorded.Blocks[i])
			}
			if result.BlockOrigin() != want {
				t.Fatalf("block %d origin = %+v, want %+v", i, result.BlockOrigin(), want)
			}
			if result.Outcome != types.Succeeded {
				t.Fatalf("block %d outcome = %v, want Succeeded", i, result.Outcome)
			}
			wantJSON, _ := json.Marshal(value)
			if len(result.Content) != 1 {
				t.Fatalf("block %d content = %d blocks, want one Text block", i, len(result.Content))
			}
			text, ok := result.Content[0].(types.Text)
			if !ok {
				t.Fatalf("block %d content[0] is %T, want Text", i, result.Content[0])
			}
			if text.Text != string(wantJSON) {
				t.Fatalf("block %d content = %q, want canonical JSON %q", i, text.Text, wantJSON)
			}
		}
	})

	record := func(t *testing.T, ctx context.Context, stack *gohan.Stack, sessionID string, q types.Quarantined[Invoice]) {
		t.Helper()
		if err := gohan.RecordQuarantined(ctx, stack, sessionID, q); err != nil {
			t.Fatalf("RecordQuarantined: %v", err)
		}
	}

	drive := func(t *testing.T, telemetry *taintEnforceTelemetry, log stores.SessionLog, recordQ bool) types.Suspended {
		t.Helper()
		hook, err := taint.New(taint.Default(), telemetry)
		if err != nil {
			t.Fatal(err)
		}
		pay := gohantest.NewFakeTool("pay_supplier", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
			spec.Effect = types.SideEffect
		}))
		model := gohantest.NewScriptedModel(types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}},
			gohantest.ToolCall("pay_supplier", map[string]any{"iban": iban, "note": "settle invoice"}),
			gohantest.Text("finished"),
		)
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			principal, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: principal}, ok
		}))
		native := gohan.NativeSpec{
			Request:     gohan.FlowRequest{Name: "chat"},
			Profile:     "native",
			Tools:       []types.Tool{pay},
			TaintPolicy: hook,
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}
		stack, err := gohan.Build(
			gohan.AllowAnonymous(),
			gohan.WithTelemetry(telemetry),
			gohan.WithStores(stores.Stores{SessionLog: log, Runs: runs, Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))}),
			gohan.WithModels(model),
			gohan.WithNativeAgent(native),
		)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "tenant", Subject: "user"})
		if recordQ {
			if _, err := log.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "read the PDF"}}}); err != nil {
				t.Fatalf("seed session: %v", err)
			}
			record(t, ctx, stack, "s1", types.Quarantined[Invoice]{Value: value, Origins: []types.Origin{pdfOrigin}})
		}
		conv, err := gohan.NewNativeConversation(stack, "chat",
			gohan.WithConversationRuns(runs),
			gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
			gohan.WithConversationApprovalPolicy(taintEnforceApprovalPolicy{}),
		)
		if err != nil {
			t.Fatalf("NewNativeConversation: %v", err)
		}
		var susp types.Suspended
		for ev, sendErr := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "settle the supplier invoice"}}}) {
			if sendErr != nil {
				t.Fatalf("Send: %v", sendErr)
			}
			if event, ok := ev.(types.Suspended); ok {
				susp = event
			}
		}
		return susp
	}

	assertAsk := func(t *testing.T, susp types.Suspended) {
		t.Helper()
		if susp.Token == "" || susp.Reason != types.HumanApproval {
			t.Fatalf("suspension = %+v, want HumanApproval", susp)
		}
		req, ok := susp.Payload.(permission.ApprovalRequest)
		if !ok {
			t.Fatalf("suspension payload = %T, want permission.ApprovalRequest", susp.Payload)
		}
		if origin := req.ArgOrigins["iban"]; origin.Kind != types.OriginTool || origin.Name != "read_pdf" {
			t.Fatalf("ArgOrigins[iban] = %+v, want read_pdf tool origin", origin)
		}
	}

	t.Run("recorded-block-in-window", func(t *testing.T) {
		susp := drive(t, &taintEnforceTelemetry{}, stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)), true)
		assertAsk(t, susp)
	})

	t.Run("taint.quarantine-carries-origin", func(t *testing.T) {
		telemetry := &taintEnforceTelemetry{}
		// The PDF blocks arrive as a tool result stamped read_pdf; the
		// recipe runs over them and the value records with that origin.
		pdfBlocks := []types.Block{
			types.Text{BlockBase: types.BlockBase{Origin: pdfOrigin}, Text: pdfText},
		}
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		model := gohantest.NewScriptedModel(types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}},
			gohantest.Text(`{"iban":"`+iban+`","name":"Nordwind GmbH"}`),
		)
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			principal, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: principal}, ok
		}))
		stack, err := gohan.Build(
			gohan.AllowAnonymous(),
			gohan.WithStores(stores.Stores{SessionLog: log, Runs: runs}),
			gohan.WithModels(model),
		)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "tenant", Subject: "user"})
		if _, err := log.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "read the PDF"}}}); err != nil {
			t.Fatalf("seed session: %v", err)
		}
		q, err := flow.Quarantine[Invoice](stack, "native").Invoke(ctx, pdfBlocks)
		if err != nil {
			t.Fatalf("Quarantine: %v", err)
		}
		if q.Value != value {
			t.Fatalf("quarantined value = %+v, want %+v", q.Value, value)
		}
		if len(q.Origins) != 1 || q.Origins[0] != pdfOrigin {
			t.Fatalf("quarantined origins = %+v, want [%+v]", q.Origins, pdfOrigin)
		}
		record(t, ctx, stack, "s1", q)
		susp := drive(t, telemetry, log, false)
		assertAsk(t, susp)
		foundAsked := false
		for _, metric := range telemetry.metrics {
			if metric.name != "gohan.taint.asked" || metric.value < 1 {
				continue
			}
			for _, attr := range metric.attrs {
				if attr.Key == "tool" && attr.Value == "pay_supplier" {
					foundAsked = true
				}
			}
		}
		if !foundAsked {
			t.Fatal("gohan.taint.asked did not count pay_supplier")
		}
	})
}
