// Command excursions runs the S1 excursion demo fully offline: a scripted
// model plans a trip through gohan's governed native conversation, the
// flow's own decider denies the booking before any call executes, and the
// denial shows up in the persisted history as a not_executed result.
// No network, no API keys.
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
)

// Trip is what one excursion run produced: the streamed events, the
// fixture tools the batch drew on, and the history the governed
// conversation persisted.
type Trip struct {
	Events  []types.Event
	Tools   []types.Tool
	History []types.Message
}

// Plan builds a stack with the scripted excursion flow registered as a
// native definition, obtains the governed native conversation for it, and
// streams one send. The conversation loads the history, gates the batch
// through the flow's decider, drives the run, and persists every append;
// the example persists nothing by hand.
func Plan(ctx context.Context, input string) (Trip, error) {
	tools := fixtureTools()
	sessions := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	stack, err := gohan.Build(
		gohan.WithStores(stores.Stores{SessionLog: sessions}),
		gohan.WithModels(fixtureModel()),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "excursions"},
			Profile: "scripted",
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: slices.Clone(in.History)}, nil
			},
			Tools: tools,
			Decider: policyDecider{
				"search_trails": permission.Allow,
				"book_cabin":    permission.DenyVerdict,
			},
		}),
	)
	if err != nil {
		return Trip{}, err
	}
	conv, err := gohan.NewNativeConversation(stack, "excursions",
		gohan.WithConversationRuns(stores.NewMemoryRuns()),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
	)
	if err != nil {
		return Trip{}, err
	}
	ctx = types.WithPrincipal(ctx, excursionPrincipal())
	var trip Trip
	for ev, err := range conv.Send(ctx, "excursion", types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: input}},
	}) {
		if err != nil {
			return trip, err
		}
		trip.Events = append(trip.Events, ev)
	}
	loaded, err := sessions.Load(ctx, "excursion")
	if err != nil {
		return trip, err
	}
	trip.Tools = tools
	trip.History = loaded.Messages
	return trip, nil
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
	for _, m := range plan.History {
		for _, b := range m.Blocks {
			if res, ok := b.(types.ToolResult); ok && res.Error != nil {
				fmt.Printf("denied: %s: %s\n", res.ID, res.Error.Message)
			}
		}
	}
}
