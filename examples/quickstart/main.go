// Command quickstart sends one scripted turn through gohan's governed
// native conversation with in-memory stores. It runs offline: no network,
// no API keys.
package main

import (
	"context"
	"fmt"
	"iter"
	"os"
	"slices"

	gohan "github.com/victorzhuk/gohan/core"
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

func assemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	return types.ModelRequest{Messages: slices.Clone(in.History)}, nil
}

// sessionLog exposes the memory log Run builds, so the tests can read
// what the governed path persisted.
var sessionLog *stores.MemorySessionLog

// Run builds a stack with the scripted model registered as a native flow,
// obtains the governed native conversation for it, and streams one send.
// The conversation loads the history, drives the run, and persists the
// assistant reply; the example persists nothing by hand.
func Run(ctx context.Context, input string) ([]types.Event, error) {
	model := &scriptedModel{chunks: []types.ModelChunk{
		{Kind: types.DeltaText, Delta: "Hello, "},
		{Kind: types.DeltaText, Delta: "quickstart!"},
	}}
	sessions := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	stack, err := gohan.Build(
		gohan.WithStores(stores.Stores{SessionLog: sessions}),
		gohan.WithModels(model),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request:  gohan.FlowRequest{Name: "quickstart"},
			Profile:  "scripted",
			Assemble: assemble,
		}),
	)
	if err != nil {
		return nil, err
	}
	conv, err := gohan.NewNativeConversation(stack, "quickstart",
		gohan.WithConversationRuns(stores.NewMemoryRuns()),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
	)
	if err != nil {
		return nil, err
	}
	ctx = types.WithPrincipal(ctx, types.Principal{
		Tenant:  "local",
		Subject: "reader",
		Scopes:  []string{types.ScopeSessionRead, types.ScopeSessionWrite},
	})
	var evs []types.Event
	for ev, err := range conv.Send(ctx, "quickstart", types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: input}},
	}) {
		if err != nil {
			return evs, err
		}
		evs = append(evs, ev)
	}
	sessionLog = sessions
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
