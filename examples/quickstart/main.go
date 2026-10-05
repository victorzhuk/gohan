// Command quickstart drives one scripted agent turn through gohan's
// native Drive loop with in-memory stores. It runs offline: no network,
// no API keys.
package main

import (
	"context"
	"fmt"
	"iter"
	"os"
	"slices"
	"strings"

	"github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// scriptedModel replays a fixed chunk sequence; the request it received
// is kept for inspection.
type scriptedModel struct {
	chunks []types.ModelChunk
	req    types.ModelRequest
}

func (m *scriptedModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "scripted"}
}

func (m *scriptedModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.req = req
	return func(yield func(types.ModelChunk, error) bool) {
		for _, c := range m.chunks {
			if !yield(c, nil) {
				return
			}
		}
	}
}

// quickstartRuntime is the native stepper for the quickstart: one Step is
// one model call whose text streams through the sink.
type quickstartRuntime struct {
	model *scriptedModel
	run   runtime.AgentRun
}

func (rt *quickstartRuntime) Name() string                         { return "quickstart.scripted" }
func (rt *quickstartRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }

func (rt *quickstartRuntime) Start(_ context.Context, r runtime.AgentRun) (runtime.State, error) {
	rt.run = r
	return runtime.State{HistoryVersion: r.History.Version}, nil
}

func (rt *quickstartRuntime) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	req, err := rt.run.Assemble(ctx, types.AssembleInput{History: rt.run.History.Messages})
	if err != nil {
		return st, nil, runtime.Continue, err
	}
	var text []string
	for chunk, err := range rt.model.Generate(ctx, req) {
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		if chunk.Kind != types.DeltaText {
			continue
		}
		text = append(text, chunk.Delta)
		if sink, ok := types.SinkFrom(ctx); ok {
			sink.Emit(ctx, types.TextDelta{Turn: st.Turn, MessageID: "assistant-1", Delta: chunk.Delta})
		}
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

func assemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	return types.ModelRequest{Messages: slices.Clone(in.History)}, nil
}

// repliedMessages holds the text of every reply the run persisted, so a
// reader can see what reached the session log.
var repliedMessages []string

// Run appends the user input to a memory session log, drives one scripted
// turn on the native runtime, and persists the assistant reply.
func Run(ctx context.Context, input string) ([]types.Event, error) {
	model := &scriptedModel{chunks: []types.ModelChunk{
		{Kind: types.DeltaText, Delta: "Hello, "},
		{Kind: types.DeltaText, Delta: "quickstart!"},
	}}
	sessions := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	if _, err := gohan.Build(
		gohan.WithStores(stores.Stores{SessionLog: sessions}),
		gohan.WithModels(model),
	); err != nil {
		return nil, err
	}
	ctx = types.WithPrincipal(ctx, types.Principal{
		Tenant:  "local",
		Subject: "reader",
		Scopes:  []string{types.ScopeSessionRead, types.ScopeSessionWrite},
	})
	ver, err := sessions.Append(ctx, "quickstart", 0,
		types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: input}}})
	if err != nil {
		return nil, err
	}
	hist, err := sessions.Load(ctx, "quickstart")
	if err != nil {
		return nil, err
	}
	rt := &quickstartRuntime{model: model}
	run := runtime.AgentRun{Model: model, History: hist, Assemble: assemble}
	var evs []types.Event
	for ev, err := range gohan.Drive(ctx, rt, run) {
		if err != nil {
			return evs, err
		}
		evs = append(evs, ev)
	}
	var reply types.Message
	for _, ev := range evs {
		if am, ok := ev.(types.AssistantMessage); ok {
			reply = am.Message
		}
	}
	if _, err := sessions.Append(ctx, "quickstart", ver, reply); err != nil {
		return evs, err
	}
	tb, _ := reply.Blocks[0].(types.Text)
	repliedMessages = append(repliedMessages, tb.Text)
	return evs, nil
}

func main() {
	evs, err := Run(context.Background(), "Say hello.")
	if err != nil {
		fmt.Fprintln(os.Stderr, "quickstart:", err)
		os.Exit(1)
	}
	for _, ev := range evs {
		if d, ok := ev.(types.TextDelta); ok {
			fmt.Print(d.Delta)
		}
	}
	fmt.Println()
}
