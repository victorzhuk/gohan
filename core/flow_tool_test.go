package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/taint"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

type subflowIn struct{}
type subflowOut struct{}

func subflowLimits(adjust func(*types.RunLimits)) types.RunLimits {
	l := types.RunLimits{
		MaxTurns:            10,
		MaxToolCalls:        10,
		MaxWallClock:        time.Minute,
		SoftRatio:           0.8,
		MaxDepth:            2,
		MaxParallelChildren: 4,
		MaxParallelTools:    4,
	}
	if adjust != nil {
		adjust(&l)
	}
	return l
}

func subflowParentCtx(depth int, runID string) context.Context {
	info := types.RunInfo{
		RunID:     runID,
		RootRunID: "run-parent",
		SessionID: "s1",
		Flow:      "parent",
		Depth:     depth,
	}
	return chains.WithLimitsState(types.WithRunInfo(context.Background(), info), chains.NewLimitsState())
}

func subflowBuild(t *testing.T, adjust func(*types.RunLimits)) *Stack {
	t.Helper()
	s, err := Build(WithLimits("parent", subflowLimits(adjust)))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return s
}

func subflowCharge(ctx context.Context, tokens int) {
	if ledger, ok := chains.LimitsStateFrom(ctx); ok {
		ledger.Charge(types.Usage{InputTokens: tokens}, types.Pricing{Input: 0.01})
	}
}

type specTool struct{ spec types.ToolSpec }

func (t specTool) Spec() types.ToolSpec { return t.spec }
func (t specTool) Call(_ context.Context, _ json.RawMessage) (types.ToolResult, error) {
	return types.ToolResult{}, nil
}

func TestSubflowsLimits(t *testing.T) {
	t.Run("subflows.max-depth", func(t *testing.T) {
		s := subflowBuild(t, nil)
		childRan := false
		child := FlowFunc[subflowIn, subflowOut]("child", func(_ context.Context, _ subflowIn) (subflowOut, error) {
			childRan = true
			return subflowOut{}, nil
		}, AllowAnonymousFlow())
		tool, err := s.FlowAsTool("child", child, WithDescription("child flow"))
		if err != nil {
			t.Fatalf("flow as tool: %v", err)
		}
		_, err = tool.Call(subflowParentCtx(2, "run-parent"), nil)
		var lerr *types.LimitExceededError
		if !errors.As(err, &lerr) {
			t.Fatalf("err %v: want LimitExceededError", err)
		}
		if lerr.Limit != "MaxDepth" {
			t.Errorf("limit %q: want MaxDepth", lerr.Limit)
		}
		if childRan {
			t.Error("child body ran past the depth limit")
		}
	})

	t.Run("missing-ledger-refused", func(t *testing.T) {
		s := subflowBuild(t, nil)
		childRan := false
		child := FlowFunc[subflowIn, subflowOut]("child", func(_ context.Context, _ subflowIn) (subflowOut, error) {
			childRan = true
			return subflowOut{}, nil
		}, AllowAnonymousFlow())
		tool, err := s.FlowAsTool("child", child, WithDescription("child flow"))
		if err != nil {
			t.Fatalf("flow as tool: %v", err)
		}
		ctx := types.WithRunInfo(context.Background(), types.RunInfo{
			RunID:     "run-parent",
			RootRunID: "run-parent",
			SessionID: "s1",
			Flow:      "parent",
		})
		_, err = tool.Call(ctx, nil)
		if err == nil {
			t.Fatal("call without a ledger passed")
		}
		if !strings.Contains(err.Error(), "ledger") {
			t.Errorf("err %q does not mention the ledger", err)
		}
		if childRan {
			t.Error("child body ran without a ledger")
		}
	})

	t.Run("subflows.child-wall-clock", func(t *testing.T) {
		s := subflowBuild(t, func(l *types.RunLimits) { l.MaxWallClock = 4 * time.Minute })
		child := FlowFunc[subflowIn, subflowOut]("child", func(_ context.Context, _ subflowIn) (subflowOut, error) {
			return subflowOut{}, nil
		}, AllowAnonymousFlow())
		tool, err := s.FlowAsTool("child", child,
			WithDescription("child flow"),
			WithChildLimits(types.ChildLimits{MaxWallClock: 10 * time.Minute}),
		)
		if err != nil {
			t.Fatalf("flow as tool: %v", err)
		}
		_, err = tool.Call(subflowParentCtx(0, "run-parent"), nil)
		if err == nil {
			t.Fatal("declaration above the parent limit passed")
		}
		if !strings.Contains(err.Error(), "child") {
			t.Errorf("err %q does not name the sub-flow tool", err)
		}
		if !strings.Contains(err.Error(), "parent") {
			t.Errorf("err %q does not name the calling flow", err)
		}
	})

	t.Run("subflows.nested-depth-allowance", func(t *testing.T) {
		s := subflowBuild(t, nil)
		grandRan := false
		grand := FlowFunc[subflowIn, subflowOut]("grand", func(_ context.Context, _ subflowIn) (subflowOut, error) {
			grandRan = true
			return subflowOut{}, nil
		}, AllowAnonymousFlow())
		grandTool, err := s.FlowAsTool("grand", grand, WithDescription("grandchild flow"))
		if err != nil {
			t.Fatalf("grand flow as tool: %v", err)
		}
		child := FlowFunc[subflowIn, subflowOut]("child", func(ctx context.Context, _ subflowIn) (subflowOut, error) {
			_, err := grandTool.Call(ctx, nil)
			return subflowOut{}, err
		}, AllowAnonymousFlow())
		childTool, err := s.FlowAsTool("child", child, WithDescription("child flow"))
		if err != nil {
			t.Fatalf("child flow as tool: %v", err)
		}
		_, err = childTool.Call(subflowParentCtx(1, "run-mid"), nil)
		var lerr *types.LimitExceededError
		if !errors.As(err, &lerr) {
			t.Fatalf("err %v: want LimitExceededError", err)
		}
		if lerr.Limit != "MaxDepth" {
			t.Errorf("limit %q: want MaxDepth", lerr.Limit)
		}
		if grandRan {
			t.Error("grandchild body ran past the depth limit")
		}
	})

	t.Run("subflows.nested-wall-clock-inheritance", func(t *testing.T) {
		parentLimits := subflowLimits(func(l *types.RunLimits) { l.MaxWallClock = 30 * time.Minute })
		s, err := Build(WithLimits("parent", parentLimits))
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		parentLedger := chains.NewLimitsState()
		if _, err := parentLedger.WallClockLeft(parentLimits, time.Now().Add(-26*time.Minute)); err != nil {
			t.Fatalf("backdate ledger: %v", err)
		}
		var got types.RunBudget
		var ok bool
		child := FlowFunc[subflowIn, subflowOut]("child", func(ctx context.Context, _ subflowIn) (subflowOut, error) {
			got, ok = types.RunBudgetFrom(ctx)
			return subflowOut{}, nil
		}, AllowAnonymousFlow())
		tool, err := s.FlowAsTool("child", child,
			WithDescription("child flow"),
			WithChildLimits(types.ChildLimits{MaxWallClock: 10 * time.Minute}),
		)
		if err != nil {
			t.Fatalf("flow as tool: %v", err)
		}
		info := types.RunInfo{
			RunID:     "run-mid",
			RootRunID: "run-parent",
			SessionID: "s1",
			Flow:      "parent",
			Depth:     1,
		}
		ctx := chains.WithLimitsState(types.WithRunInfo(context.Background(), info), parentLedger)
		if _, err := tool.Call(ctx, nil); err != nil {
			t.Fatalf("child call: %v", err)
		}
		if !ok {
			t.Fatal("no run budget in child ctx")
		}
		if got.WallClock <= 0 || got.WallClock > 4*time.Minute || got.WallClock <= 3*time.Minute {
			t.Errorf("inherited wall clock %s: want in (3m, 4m]", got.WallClock)
		}
	})

	t.Run("subflow nested derivation clamps to the inherited wall clock", func(t *testing.T) {
		parentLimits := subflowLimits(func(l *types.RunLimits) {
			l.MaxWallClock = 30 * time.Minute
			l.MaxDepth = 3
		})
		s, err := Build(WithLimits("parent", parentLimits))
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		parentLedger := chains.NewLimitsState()
		if _, err := parentLedger.WallClockLeft(parentLimits, time.Now().Add(-26*time.Minute)); err != nil {
			t.Fatalf("backdate ledger: %v", err)
		}
		var grandBudget types.RunBudget
		var grandOK bool
		grand := FlowFunc[subflowIn, subflowOut]("grand", func(ctx context.Context, _ subflowIn) (subflowOut, error) {
			grandBudget, grandOK = types.RunBudgetFrom(ctx)
			return subflowOut{}, nil
		}, AllowAnonymousFlow())
		grandTool, err := s.FlowAsTool("grand", grand,
			WithDescription("grandchild flow"),
			WithChildLimits(types.ChildLimits{MaxWallClock: 20 * time.Minute}),
		)
		if err != nil {
			t.Fatalf("grand flow as tool: %v", err)
		}
		child := FlowFunc[subflowIn, subflowOut]("child", func(ctx context.Context, _ subflowIn) (subflowOut, error) {
			_, err := grandTool.Call(ctx, nil)
			return subflowOut{}, err
		}, AllowAnonymousFlow())
		childTool, err := s.FlowAsTool("child", child,
			WithDescription("child flow"),
			WithChildLimits(types.ChildLimits{MaxWallClock: 10 * time.Minute}),
		)
		if err != nil {
			t.Fatalf("child flow as tool: %v", err)
		}
		info := types.RunInfo{
			RunID:     "run-mid",
			RootRunID: "run-parent",
			SessionID: "s1",
			Flow:      "parent",
			Depth:     1,
		}
		ctx := chains.WithLimitsState(types.WithRunInfo(context.Background(), info), parentLedger)
		if _, err := childTool.Call(ctx, nil); err != nil {
			t.Fatalf("child call: %v", err)
		}
		if !grandOK {
			t.Fatal("no run budget in grandchild ctx")
		}
		if grandBudget.WallClock <= 3*time.Minute || grandBudget.WallClock > 4*time.Minute {
			t.Errorf("grandchild wall clock %s: want in (3m, 4m]", grandBudget.WallClock)
		}
	})

	t.Run("subflows.fixed-cost-share", func(t *testing.T) {
		s := subflowBuild(t, func(l *types.RunLimits) { l.MaxCost = 0.50 })
		child := FlowFunc[subflowIn, subflowOut]("child", func(ctx context.Context, _ subflowIn) (subflowOut, error) {
			subflowCharge(ctx, 12)
			return subflowOut{}, nil
		}, AllowAnonymousFlow())
		tool, err := s.FlowAsTool("child", child,
			WithDescription("child flow"),
			WithChildLimits(types.ChildLimits{CostShare: types.ShareFixed, FixedCost: 0.10}),
		)
		if err != nil {
			t.Fatalf("flow as tool: %v", err)
		}
		parentLedger := chains.NewLimitsState()
		ctx := chains.WithLimitsState(subflowParentCtx(0, "run-parent"), parentLedger)
		_, err = tool.Call(ctx, nil)
		var lerr *types.LimitExceededError
		if !errors.As(err, &lerr) {
			t.Fatalf("err %v: want LimitExceededError", err)
		}
		if lerr.Limit != "MaxCost" {
			t.Errorf("limit %q: want MaxCost", lerr.Limit)
		}
		if spent := parentLedger.TreeCost(); math.Abs(spent-0.12) > 1e-9 {
			t.Errorf("tree cost %v: want 0.12", spent)
		}
	})

	t.Run("subflows.nested-cost-inheritance", func(t *testing.T) {
		s := subflowBuild(t, func(l *types.RunLimits) { l.MaxCost = 0.50 })
		grandRan := false
		grand := FlowFunc[subflowIn, subflowOut]("grand", func(ctx context.Context, _ subflowIn) (subflowOut, error) {
			grandRan = true
			subflowCharge(ctx, 25)
			return subflowOut{}, nil
		}, AllowAnonymousFlow())
		grandTool, err := s.FlowAsTool("grand", grand, WithDescription("grandchild flow"))
		if err != nil {
			t.Fatalf("grand flow as tool: %v", err)
		}
		child := FlowFunc[subflowIn, subflowOut]("child", func(ctx context.Context, _ subflowIn) (subflowOut, error) {
			subflowCharge(ctx, 30)
			_, err := grandTool.Call(ctx, nil)
			return subflowOut{}, err
		}, AllowAnonymousFlow())
		childTool, err := s.FlowAsTool("child", child, WithDescription("child flow"))
		if err != nil {
			t.Fatalf("child flow as tool: %v", err)
		}
		siblingRan := false
		sibling := FlowFunc[subflowIn, subflowOut]("sibling", func(_ context.Context, _ subflowIn) (subflowOut, error) {
			siblingRan = true
			return subflowOut{}, nil
		}, AllowAnonymousFlow())
		siblingTool, err := s.FlowAsTool("sibling", sibling, WithDescription("sibling flow"))
		if err != nil {
			t.Fatalf("sibling flow as tool: %v", err)
		}
		ctx := subflowParentCtx(0, "run-parent")
		_, err = childTool.Call(ctx, nil)
		var lerr *types.LimitExceededError
		if !errors.As(err, &lerr) {
			t.Fatalf("err %v: want LimitExceededError", err)
		}
		if !grandRan {
			t.Error("grandchild body did not run")
		}
		if _, err := siblingTool.Call(ctx, nil); err != nil {
			t.Errorf("sibling call after overrun: %v", err)
		}
		if !siblingRan {
			t.Error("parent did not continue after the overrun")
		}
	})

	t.Run("a suspending child without a checkpoint store fails as not suspendable", func(t *testing.T) {
		s := subflowBuild(t, nil)
		runs := stores.NewMemoryRuns()
		s.stores.Runs = runs
		var childRunID string
		child := FlowFunc[subflowIn, subflowOut]("child", func(ctx context.Context, _ subflowIn) (subflowOut, error) {
			info, _ := types.RunInfoFrom(ctx)
			childRunID = info.RunID
			return subflowOut{}, &types.SuspendError{Token: "tok-child", Reason: types.AwaitingInput}
		}, AllowAnonymousFlow())
		tool, err := s.FlowAsTool("child", child, WithDescription("child flow"))
		if err != nil {
			t.Fatalf("flow as tool: %v", err)
		}
		_, err = tool.Call(subflowParentCtx(0, "run-parent"), nil)
		if !errors.Is(err, types.ErrNotSuspendable) {
			t.Fatalf("err %v: want ErrNotSuspendable", err)
		}
		row, err := runs.ByID(context.Background(), childRunID)
		if err != nil {
			t.Fatalf("read child run %s: %v", childRunID, err)
		}
		if row.State == stores.Running {
			t.Errorf("child run left Running after the refused suspension")
		}
	})

	t.Run("a slow child does not fail on lease expiry", func(t *testing.T) {
		// The child outlasts stores.LeaseTTL in real time; the heartbeat
		// goroutine must keep the lease alive for the row to finish.
		s := subflowBuild(t, nil)
		runs := stores.NewMemoryRuns()
		s.stores.Runs = runs
		var childRunID string
		child := FlowFunc[subflowIn, subflowOut]("child", func(ctx context.Context, _ subflowIn) (subflowOut, error) {
			info, _ := types.RunInfoFrom(ctx)
			childRunID = info.RunID
			time.Sleep(stores.LeaseTTL + 500*time.Millisecond)
			return subflowOut{}, nil
		}, AllowAnonymousFlow())
		tool, err := s.FlowAsTool("child", child, WithDescription("child flow"))
		if err != nil {
			t.Fatalf("flow as tool: %v", err)
		}
		if _, err := tool.Call(subflowParentCtx(0, "run-parent"), nil); err != nil {
			t.Fatalf("slow child call: %v", err)
		}
		row, err := runs.ByID(context.Background(), childRunID)
		if err != nil {
			t.Fatalf("read child run %s: %v", childRunID, err)
		}
		if row.State != stores.Finished {
			t.Errorf("child run state %v: want Finished", row.State)
		}
	})

	t.Run("subflows.effect-derived", func(t *testing.T) {
		s, err := Build(
			WithModels(&funcModel{}),
			WithLimits("parent", subflowLimits(nil)),
			WithNativeAgent(NativeSpec{
				Request: FlowRequest{Name: "childflow"},
				Profile: "native",
				Tools: []types.Tool{
					specTool{spec: types.ToolSpec{Name: "read", Effect: types.ReadOnly}},
					specTool{spec: types.ToolSpec{Name: "write", Effect: types.SideEffect}},
				},
			}),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		childRan := false
		child := FlowFunc[subflowIn, subflowOut]("childflow", func(_ context.Context, _ subflowIn) (subflowOut, error) {
			childRan = true
			return subflowOut{}, nil
		}, AllowAnonymousFlow())
		tool, err := s.FlowAsTool("child", child, WithDescription("child flow"))
		if err != nil {
			t.Fatalf("flow as tool: %v", err)
		}
		spec := tool.Spec()
		if spec.Effect != types.SideEffect {
			t.Errorf("subflow tool effect %v: want SideEffect", spec.Effect)
		}
		dec := permission.DecideCall(context.Background(), nil, &permission.ToolInvocation{
			Spec: spec,
			Call: types.ToolUse{ID: "c1", Name: spec.Name},
		})
		if dec.Verdict != permission.Ask {
			t.Errorf("parent gate verdict %v: want Ask for a SideEffect sub-flow call", dec.Verdict)
		}
		if childRan {
			t.Error("child body ran before the parent gate allowed the call")
		}
	})

	t.Run("subflows.effect-tighten-only", func(t *testing.T) {
		s, err := Build(
			WithModels(&funcModel{}),
			WithLimits("parent", subflowLimits(nil)),
			WithNativeAgent(NativeSpec{
				Request: FlowRequest{Name: "childflow"},
				Profile: "native",
				Tools: []types.Tool{
					specTool{spec: types.ToolSpec{Name: "read", Effect: types.ReadOnly}},
					specTool{spec: types.ToolSpec{Name: "write", Effect: types.SideEffect}},
				},
			}),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		child := FlowFunc[subflowIn, subflowOut]("childflow", func(_ context.Context, _ subflowIn) (subflowOut, error) {
			return subflowOut{}, nil
		}, AllowAnonymousFlow())
		_, err = s.FlowAsTool("child", child,
			WithDescription("child flow"),
			WithChildEffect(types.ReadOnly),
		)
		if err == nil {
			t.Fatal("tightening a SideEffect child to ReadOnly passed")
		}
		if !strings.Contains(err.Error(), "ReadOnly") || !strings.Contains(err.Error(), "SideEffect") {
			t.Errorf("err %q does not name the effect conflict", err)
		}
	})
}

type subflowTaintMetric struct {
	name  string
	value int64
	attrs []types.Attr
}

type subflowTaintTelemetry struct {
	metrics []subflowTaintMetric
}

func (m *subflowTaintTelemetry) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (m *subflowTaintTelemetry) Count(_ context.Context, name string, value int64, attrs ...types.Attr) {
	m.metrics = append(m.metrics, subflowTaintMetric{name: name, value: value, attrs: append([]types.Attr(nil), attrs...)})
}

func (m *subflowTaintTelemetry) Record(_ context.Context, _ string, _ float64, _ ...types.Attr) {}

type subflowTaintApprovalPolicy struct{}

func (subflowTaintApprovalPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{}, nil
}

type subflowInvoice struct {
	IBAN string `json:"iban"`
	Name string `json:"name"`
}

func TestSubflowTaint(t *testing.T) {
	t.Run("taint.subflow-carries-origin", func(t *testing.T) {
		const iban = "DE44500105175407324931"
		mailOrigin := types.Origin{Kind: types.OriginProvider, Name: "mail"}
		telemetry := &subflowTaintTelemetry{}
		hook, err := taint.New(taint.Default(), telemetry)
		if err != nil {
			t.Fatal(err)
		}
		child := FlowFunc[subflowIn, types.Quarantined[subflowInvoice]]("child", func(_ context.Context, _ subflowIn) (types.Quarantined[subflowInvoice], error) {
			return types.Quarantined[subflowInvoice]{
				Value:   subflowInvoice{IBAN: iban, Name: "Nordwind GmbH"},
				Origins: []types.Origin{mailOrigin},
			}, nil
		}, AllowAnonymousFlow())
		pay := gohantest.NewFakeTool("pay_supplier", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
			spec.Effect = types.SideEffect
		}))
		model := gohantest.NewScriptedModel(types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}},
			gohantest.ToolCall("child", map[string]any{}),
			gohantest.ToolCall("pay_supplier", map[string]any{"iban": iban, "note": "settle invoice"}),
			gohantest.Text("finished"),
		)
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			principal, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: principal}, ok
		}))
		checkpoints := stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))
		storesFor := func() stores.Stores {
			return stores.Stores{SessionLog: log, Runs: runs, Checkpoints: checkpoints}
		}
		// FlowAsTool resolves against a stack; the seed stack shares the
		// stores and limits the parent run uses so the child books into the
		// same run tree.
		seedStack, err := Build(AllowAnonymous(), WithLimits("chat", subflowLimits(nil)), WithStores(storesFor()))
		if err != nil {
			t.Fatalf("seed build: %v", err)
		}
		childTool, err := seedStack.FlowAsTool("child", child, WithDescription("extract the invoice from the mail"))
		if err != nil {
			t.Fatalf("flow as tool: %v", err)
		}
		native := NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "native",
			Tools:   []types.Tool{childTool, pay},
			TaintPolicy: hook,
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}
		stack, err := Build(
			AllowAnonymous(),
			WithTelemetry(telemetry),
			WithLimits("chat", subflowLimits(nil)),
			WithStores(storesFor()),
			WithModels(model),
			WithNativeAgent(native),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		conv, err := NewNativeConversation(stack, "chat",
			WithConversationRuns(runs),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
			WithConversationApprovalPolicy(subflowTaintApprovalPolicy{}),
		)
		if err != nil {
			t.Fatalf("NewNativeConversation: %v", err)
		}
		ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "tenant", Subject: "user"})
		var susp types.Suspended
		for ev, sendErr := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "settle the supplier invoice"}}}) {
			if sendErr != nil {
				t.Fatalf("Send: %v", sendErr)
			}
			if event, ok := ev.(types.Suspended); ok {
				susp = event
			}
		}
		if susp.Reason != types.HumanApproval {
			t.Fatalf("suspension = %+v, want HumanApproval", susp)
		}
		req, ok := susp.Payload.(permission.ApprovalRequest)
		if !ok {
			t.Fatalf("suspension payload = %T, want permission.ApprovalRequest", susp.Payload)
		}
		origin := req.ArgOrigins["iban"]
		if origin != mailOrigin {
			t.Fatalf("ArgOrigins[iban] = %+v, want the child's provider origin %+v", origin, mailOrigin)
		}
		if origin.Kind == types.OriginTool && origin.Name == "child" {
			t.Fatalf("ArgOrigins[iban] = %+v: the subflow tool origin must not replace the child's source origin", origin)
		}
	})
}
