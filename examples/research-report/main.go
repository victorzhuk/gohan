// Command research-report runs an offline research-report demo: a scripted
// model gathers two archived sources through gohan's governed native
// conversation, the context policy clears spent tool results and folds the
// covered prefix into a summary, and the persisted history is loaded back
// through the session log at the end. No network, no API keys.
package main

import (
	"context"
	"fmt"
	"os"
	"slices"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
	stdcontext "github.com/victorzhuk/gohan/std/context"
)

// Report is what one research-report run produced: the streamed events,
// the fixture tools the batch drew on, and the history the governed
// conversation persisted.
type Report struct {
	Events  []types.Event
	Tools   []types.Tool
	History []types.Message
}

// Compose builds a stack with the scripted research flow registered as a
// native definition, obtains the governed native conversation for it, and
// streams one send. The context policy projects spent tool results away
// and summarizes the covered prefix; the example persists nothing by hand.
func Compose(ctx context.Context, topic string) (Report, error) {
	tools := fixtureTools()
	sessions := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	stack, err := gohan.Build(append(std.Defaults(),
		gohan.WithStores(stores.Stores{SessionLog: sessions}),
		gohan.WithModels(fixtureModel()),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "research_report"},
			Profile: "scripted",
			ContextPolicy: stores.ContextPolicy{
				Projections: []stores.Projection{stdcontext.ClearToolResults{}},
				Compactor: stdcontext.NewSummarize(
					stdcontext.WithSummarizeSource(func(_ context.Context, h stores.History, _ types.ModelProfile) ([]types.Block, error) {
						return []types.Block{types.Text{Text: fmt.Sprintf("Research so far on %s: %d messages covered.", topic, len(h.Messages))}}, nil
					}),
				),
			},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: slices.Clone(in.History)}, nil
			},
			Tools: tools,
			Decider: policyDecider{
				"search_archive": permission.Allow,
				"take_notes":     permission.Allow,
			},
		}),
	)...)
	if err != nil {
		return Report{}, err
	}
	conv, err := gohan.NewNativeConversation(stack, "research_report",
		gohan.WithConversationRuns(stores.NewMemoryRuns()),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
	)
	if err != nil {
		return Report{}, err
	}
	ctx = types.WithPrincipal(ctx, reportPrincipal())
	var report Report
	for ev, err := range conv.Send(ctx, "research", types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: topic}},
	}) {
		if err != nil {
			return report, err
		}
		report.Events = append(report.Events, ev)
	}
	loaded, err := sessions.Load(ctx, "research")
	if err != nil {
		return report, err
	}
	report.Tools = tools
	report.History = loaded.Messages
	return report, nil
}

func main() {
	report, err := Compose(context.Background(), "Research signal pipelines: search the archive and take notes.")
	if err != nil {
		fmt.Fprintln(os.Stderr, "research-report:", err)
		os.Exit(1)
	}
	for _, ev := range report.Events {
		if d, ok := ev.(types.TextDelta); ok {
			fmt.Print(d.Delta)
		}
	}
	fmt.Println()
	for _, m := range report.History {
		for _, b := range m.Blocks {
			if res, ok := b.(types.ToolResult); ok {
				fmt.Printf("source: %s: %v\n", res.ID, res.Outcome)
			}
		}
	}
}
