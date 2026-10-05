package gohan

import (
	"context"
	"iter"
	"testing"

	"encoding/json/jsontext"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func nlUser(text string) types.Message {
	return types.Message{ID: "u-1", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: text}}}
}

func nlFinalModel(reply string, charge func(context.Context)) types.ModelFunc {
	return func(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			if charge != nil {
				charge(ctx)
			}
			yield(types.ModelChunk{Delta: reply, Finish: types.FinishStop}, nil)
		}
	}
}

func nlToolCallModel(name string) types.ModelFunc {
	return func(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			yield(types.ModelChunk{
				Kind: types.DeltaToolArgs,
				ToolUse: &types.ToolUse{
					ID:   "call-1",
					Name: name,
					Args: jsontext.Value("{}"),
				},
			}, nil)
		}
	}
}

func nlAssembleCapture(saw *[][]types.Message) func(context.Context, types.AssembleInput) (types.ModelRequest, error) {
	return func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
		*saw = append(*saw, in.History)
		return types.ModelRequest{}, nil
	}
}

func nlExecOK(_ context.Context, _ types.ToolUse) (types.ToolResult, error) {
	return types.ToolResult{Content: []types.Block{types.Text{Text: "ok"}}}, nil
}

func nlCountDones(collected *[]types.Done) func(types.Event, error) bool {
	return func(ev types.Event, err error) bool {
		if err != nil {
			return false
		}
		if d, ok := ev.(types.Done); ok {
			*collected = append(*collected, d)
		}
		return true
	}
}

// A steer signaled while a batch runs is drained at the post-batch safe
// point, appended to history and folded into the next model request; the
// run then finishes once and Done follows Finish.
func TestNativeLifecycleSteerDuringBatch(t *testing.T) {
	runs := &lcRuns{rec: &order{}}
	lease, err := runs.Start(context.Background(), stores.Run{SessionID: "s1", RunID: "run-1"}, stores.LeaseTTL)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := runs.Signal(context.Background(), "run-1", stores.Signal{
		Kind:    stores.SignalSteer,
		Message: nlUser("steer-now"),
	}); err != nil {
		t.Fatalf("signal: %v", err)
	}

	var saw [][]types.Message
	toolModel := nlToolCallModel("noop")
	model := func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		if len(saw) == 0 {
			return toolModel(ctx, req)
		}
		return nlFinalModel("done", nil)(ctx, req)
	}
	history := []types.Message{nlUser("hello")}
	tc := turnConfig{
		model:    model,
		assemble: nlAssembleCapture(&saw),
		history:  history,
		exec:     nlExecOK,
	}
	ag := nativeRun(tc, history, nil)
	ag.Save = func(context.Context, stores.Checkpoint) (types.ResumeToken, error) {
		return types.ResumeToken("t"), nil
	}

	lc := NewLifecycle(
		WithLifecycleRuns(runs, lease),
		WithLifecycleAppender(AppendFunc(func(_ context.Context, expected int64, msgs ...types.Message) (int64, error) {
			return expected + int64(len(msgs)), nil
		})),
	)
	var dones []types.Done
	for ev, err := range DriveLifecycle(context.Background(), lc, runtime.NewNative(), ag) {
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		nlCountDones(&dones)(ev, nil)
	}
	if len(saw) != 2 {
		t.Fatalf("assemblies = %d, want 2", len(saw))
	}
	if len(saw[0]) != 1 {
		t.Fatalf("first request history = %d messages, want the initial one", len(saw[0]))
	}
	if len(saw[1]) < 2 {
		t.Fatalf("post-steer request history = %d messages, want the steer folded in", len(saw[1]))
	}
	last := saw[1][len(saw[1])-1]
	if txt, ok := last.Blocks[0].(types.Text); !ok || txt.Text != "steer-now" {
		t.Fatalf("post-steer request last message = %v, want the steer text", last.Blocks)
	}
	if len(dones) == 0 {
		t.Fatal("drive emitted no Done")
	}
	final := dones[len(dones)-1]
	if final.Reason != types.StopCompleted {
		t.Fatalf("Done reason = %v, want StopCompleted", final.Reason)
	}
	if runs.finished != 1 {
		t.Fatalf("Finish calls = %d, want 1 (Done follows Finish)", runs.finished)
	}
	runs.rec.want(t, "runs.finish")
}

// A resumed run keeps charging the ledger it already spent: the second
// drive carries the same ledger in its context, the spend accumulates and
// the terminal Done projects the accrued tree cost.
func TestNativeLifecycleResumeKeepsSpentCost(t *testing.T) {
	ledger := chains.NewLimitsState()
	price := types.Pricing{Input: 0.01}
	charge := func(ctx context.Context) {
		if st, ok := chains.LimitsStateFrom(ctx); ok {
			st.Charge(types.Usage{InputTokens: 100}, price)
		}
	}
	calls := 0
	model := func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		calls++
		charge(ctx)
		if calls == 1 {
			return nlToolCallModel("noop")(ctx, req)
		}
		return nlFinalModel("done", nil)(ctx, req)
	}
	assemble := func(context.Context, types.AssembleInput) (types.ModelRequest, error) {
		return types.ModelRequest{}, nil
	}
	gate := func(context.Context, types.ToolUse) runtime.BatchDecision {
		return runtime.BatchDecision{Outcome: runtime.BatchAsk}
	}
	history := []types.Message{nlUser("go")}
	tc := turnConfig{
		model:    model,
		assemble: assemble,
		history:  history,
		gate:     gate,
	}
	ag := nativeRun(tc, history, nil)
	var resumed runtime.State
	inner := ag.BatchEffect
	ag.BatchEffect = func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
		out, evs, status, err := inner(ctx, st)
		resumed = out
		return out, evs, status, err
	}
	ag.Save = func(context.Context, stores.Checkpoint) (types.ResumeToken, error) {
		return types.ResumeToken("t"), nil
	}

	noopSpec := func(string) (types.ToolSpec, bool) {
		return types.ToolSpec{Name: "noop", Effect: types.SideEffect}, true
	}
	lc1 := NewLifecycle(
		WithLifecycleLease(stores.Lease{RunID: "run-1"}),
		WithLifecycleLedger(ledger),
		WithLifecycleToolSpecs(noopSpec),
		WithLifecycleApprovalPolicy(&authPolicySource{}),
	)
	var suspended bool
	for _, err := range DriveLifecycle(context.Background(), lc1, runtime.NewNative(), ag) {
		if err != nil {
			t.Fatalf("first drive: %v", err)
		}
		suspended = true
	}
	if !suspended {
		t.Fatal("first drive never suspended")
	}
	spent := ledger.Cost()
	if spent == 0 {
		t.Fatal("first run charged nothing")
	}

	calls = 0
	lc2 := NewLifecycle(
		WithLifecycleLease(stores.Lease{RunID: "run-1"}),
		WithLifecycleLedger(ledger),
		WithLifecycleToolSpecs(noopSpec),
		WithLifecycleApprovalPolicy(&authPolicySource{}),
		WithLifecycleResumeState(resumed),
	)
	tc2 := turnConfig{
		model:    nlFinalModel("done", charge),
		assemble: assemble,
		history:  history,
	}
	ag2 := nativeRun(tc2, history, nil)
	ag2.Save = ag.Save
	var dones []types.Done
	for ev, err := range DriveLifecycle(context.Background(), lc2, runtime.NewNative(), ag2) {
		if err != nil {
			t.Fatalf("resumed drive: %v", err)
		}
		if d, ok := ev.(types.Done); ok {
			dones = append(dones, d)
		}
	}
	if ledger.Cost() <= spent {
		t.Fatalf("ledger cost = %v, want more than the pre-resume %v", ledger.Cost(), spent)
	}
	if got := ledger.TreeCost(); got != ledger.Cost() {
		t.Fatalf("tree cost = %v, want the local %v", got, ledger.Cost())
	}
	var done types.Done
	for _, d := range dones {
		done = d
	}
	if done.Cost != ledger.TreeCost() {
		t.Fatalf("Done.Cost = %v, want the accrued %v", done.Cost, ledger.TreeCost())
	}
}

// A run without a ledger still finishes with the same Done it produced
// before the cost projection existed.
func TestNativeLifecycleNoLedgerDoneUnchanged(t *testing.T) {
	history := []types.Message{nlUser("hi")}
	tc := turnConfig{
		model:    nlFinalModel("done", nil),
		assemble: nlAssembleCapture(&[][]types.Message{}),
		history:  history,
	}
	ag := nativeRun(tc, history, nil)
	var dones []types.Done
	for ev, err := range DriveLifecycle(context.Background(), NewLifecycle(), runtime.NewNative(), ag) {
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		if d, ok := ev.(types.Done); ok {
			dones = append(dones, d)
		}
	}
	if len(dones) == 0 {
		t.Fatal("drive emitted no Done")
	}
	final := dones[len(dones)-1]
	if final.Reason != types.StopCompleted || final.Cost != 0 {
		t.Fatalf("Done = %+v, want StopCompleted with zero cost", final)
	}
}
