// Command excursions runs the S1 excursion demo fully offline: a scripted
// model plans a trip, a fixture-backed tool batch is gated by a fixture
// permission policy, and the denied booking is visible in the appended
// history as a not_executed result. No network, no API keys.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// tripRuntime drives the excursion demo on the native stepper: turn one
// is the gated tool batch, turn two the final text reply.
type tripRuntime struct {
	model    types.Model
	tools    []types.Tool
	policy   policyDecider
	runInfo  types.RunInfo
	run      runtime.AgentRun
	appended []types.Message
	report   runtime.BatchReport
}

func (rt *tripRuntime) Name() string                         { return "excursions.scripted" }
func (rt *tripRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }

func (rt *tripRuntime) Start(_ context.Context, r runtime.AgentRun) (runtime.State, error) {
	rt.run = r
	return runtime.State{HistoryVersion: r.History.Version}, nil
}

func (rt *tripRuntime) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	req, err := rt.run.Assemble(ctx, types.AssembleInput{History: rt.run.History.Messages})
	if err != nil {
		return st, nil, runtime.Continue, err
	}
	var (
		calls   []types.ToolUse
		text    []string
		hadTool bool
	)
	for chunk, err := range rt.model.Generate(ctx, req) {
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		if chunk.ToolUse != nil {
			calls = append(calls, *chunk.ToolUse)
			hadTool = true
		}
		if chunk.Kind == types.DeltaText {
			text = append(text, chunk.Delta)
			if sink, ok := types.SinkFrom(ctx); ok {
				sink.Emit(ctx, types.TextDelta{Turn: st.Turn, MessageID: "assistant-1", Delta: chunk.Delta})
			}
		}
	}
	if hadTool {
		return rt.runBatch(ctx, st, calls)
	}
	msg := types.Message{
		ID:   "assistant-1",
		Role: types.RoleAssistant,
		Blocks: []types.Block{
			types.Text{Text: strings.Join(text, "")},
		},
	}
	if sink, ok := types.SinkFrom(ctx); ok {
		sink.Emit(ctx, types.AssistantMessage{Turn: st.Turn, Message: msg})
	}
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: rt.run.History.Version + 1},
		nil, runtime.DoneStatus, nil
}

// runBatch gates every call before the first one executes, appends the
// assistant message with its pending calls ahead of the batch, and the
// settled results afterwards.
func (rt *tripRuntime) runBatch(ctx context.Context, st runtime.State, calls []types.ToolUse) (runtime.State, []types.Event, runtime.Status, error) {
	pending := types.Message{ID: "assistant-1", Role: types.RoleAssistant}
	for _, call := range calls {
		pending.Blocks = append(pending.Blocks, call)
	}
	rt.appended = append(rt.appended, pending)
	report, err := runtime.Batch{
		Calls:  calls,
		Limits: types.RunLimits{MaxToolCalls: 8},
		Gate:   rt.gate,
		Exec:   rt.exec,
	}.Run(ctx)
	if err != nil {
		return st, nil, runtime.Continue, err
	}
	results := types.Message{Role: types.RoleAssistant}
	for _, br := range report.Results {
		res := br.Result
		res.ID = br.Call.ID
		results.Blocks = append(results.Blocks, res)
	}
	rt.appended = append(rt.appended, results)
	rt.report = report
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: rt.run.History.Version + 2},
		nil, runtime.Continue, nil
}

// gate wires the fixture policy through the permission seam so a reader
// sees the same deny verdict the governed stack would take.
func (rt *tripRuntime) gate(ctx context.Context, call types.ToolUse) runtime.BatchDecision {
	spec, ok := rt.specOf(call.Name)
	if !ok {
		return runtime.BatchDecision{Outcome: runtime.BatchDeny, Reason: "unknown tool"}
	}
	inv := &permission.ToolInvocation{Spec: spec, Call: call, Run: rt.runInfo}
	d := permission.DecideCall(ctx, rt.policy, inv,
		permission.WithSpecLookup(rt.specOf),
		permission.WithRunInfo(func(context.Context) types.RunInfo { return rt.runInfo }),
	)
	switch d.Verdict {
	case permission.Allow:
		return runtime.BatchDecision{Outcome: runtime.BatchAllow}
	case permission.DenyVerdict:
		return runtime.BatchDecision{Outcome: runtime.BatchDeny, Reason: d.Reason}
	case permission.TaintDenied:
		return runtime.BatchDecision{Outcome: runtime.BatchTaintDenied, Reason: d.Reason}
	default:
		return runtime.BatchDecision{Outcome: runtime.BatchAsk}
	}
}

func (rt *tripRuntime) specOf(name string) (types.ToolSpec, bool) {
	for _, t := range rt.tools {
		if s := t.Spec(); s.Name == name {
			return s, true
		}
	}
	return types.ToolSpec{}, false
}

func (rt *tripRuntime) exec(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
	for _, t := range rt.tools {
		if t.Spec().Name == call.Name {
			return t.Call(ctx, json.RawMessage(call.Args))
		}
	}
	return types.ToolResult{}, fmt.Errorf("gohan: unknown tool %q", call.Name)
}

// Trip is what one excursion run produced: the streamed events, the
// batch report with the settled permission verdicts, and the messages
// the run appended to the session log.
type Trip struct {
	Events   []types.Event
	Batch    runtime.BatchReport
	History  []types.Message
	Appended []types.Message
}

// Plan appends the user input to a memory session log and drives the
// scripted excursion on the native runtime.
func Plan(ctx context.Context, input string) (Trip, error) {
	model := fixtureModel()
	tools := fixtureTools()
	sessions := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	if _, err := gohan.Build(
		gohan.WithStores(stores.Stores{SessionLog: sessions}),
		gohan.WithModels(model),
	); err != nil {
		return Trip{}, err
	}
	ctx = types.WithPrincipal(ctx, excursionPrincipal())
	ver, err := sessions.Append(ctx, "excursion", 0,
		types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: input}}})
	if err != nil {
		return Trip{}, err
	}
	hist, err := sessions.Load(ctx, "excursion")
	if err != nil {
		return Trip{}, err
	}
	rt := &tripRuntime{
		model:  model,
		tools:  tools,
		policy: policyDecider{"search_trails": permission.Allow, "book_cabin": permission.DenyVerdict},
		runInfo: types.RunInfo{
			Flow:      "excursions",
			SessionID: "excursion",
			RunID:     "run-1",
			Principal: excursionPrincipal(),
		},
	}
	run := runtime.AgentRun{Model: model, Tools: tools, History: hist, Assemble: assemble}
	var trip Trip
	for ev, err := range gohan.Drive(ctx, rt, run) {
		if err != nil {
			return trip, err
		}
		trip.Events = append(trip.Events, ev)
	}
	for _, m := range rt.appended {
		if _, err := sessions.Append(ctx, "excursion", ver, m); err != nil {
			return trip, err
		}
		ver++
	}
	trip.Appended = rt.appended
	trip.Batch = rt.report
	loaded, err := sessions.Load(ctx, "excursion")
	if err != nil {
		return trip, err
	}
	trip.History = loaded.Messages
	return trip, nil
}

func assemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	return types.ModelRequest{Messages: slices.Clone(in.History)}, nil
}

func main() {
	plan, err := Plan(context.Background(), "Plan a weekend in the Dolomites: find trails and book a cabin.")
	if err != nil {
		fmt.Fprintln(os.Stderr, "excursions:", err)
		os.Exit(1)
	}
	for _, ev := range plan.Events {
		if d, ok := ev.(types.TextDelta); ok {
			fmt.Print(d.Delta)
		}
	}
	fmt.Println()
	for _, m := range plan.Appended {
		for _, b := range m.Blocks {
			if res, ok := b.(types.ToolResult); ok && res.Error != nil {
				fmt.Printf("denied: %s: %s\n", res.ID, res.Error.Message)
			}
		}
	}
}
