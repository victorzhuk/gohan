package main

import (
	"context"
	"slices"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func collectEvents(t *testing.T, input string) []types.Event {
	t.Helper()
	evs, err := Run(context.Background(), input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return evs
}

func TestQuickstartOffline(t *testing.T) {
	t.Run("native turn with memory stores", func(t *testing.T) {
		evs := collectEvents(t, "Say hello.")
		if len(evs) != 4 {
			t.Fatalf("got %d events, want 4: %v", len(evs), evs)
		}
		deltas := []string{"Hello, ", "quickstart!"}
		for i, want := range deltas {
			d, ok := evs[i].(types.TextDelta)
			if !ok || d.Delta != want {
				t.Fatalf("event %d is %v, want TextDelta %q", i, evs[i], want)
			}
		}
		am, ok := evs[2].(types.AssistantMessage)
		if !ok || len(am.Message.Blocks) != 1 {
			t.Fatalf("event 2 is %v, want AssistantMessage with one block", evs[2])
		}
		tb, ok := am.Message.Blocks[0].(types.Text)
		if !ok || tb.Text != "Hello, quickstart!" {
			t.Fatalf("assistant block %v, want Text %q", am.Message.Blocks[0], "Hello, quickstart!")
		}
		if _, ok := evs[3].(types.Done); !ok {
			t.Fatalf("event 3 is %v, want types.Done", evs[3])
		}
	})

	t.Run("scripted model receives session history", func(t *testing.T) {
		m := &scriptedModel{chunks: []types.ModelChunk{{Kind: types.DeltaText, Delta: "ok"}}}
		req, err := assemble(context.Background(), types.AssembleInput{
			History: []types.Message{{Role: types.RoleUser}},
		})
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		for _, err := range m.Generate(context.Background(), req) {
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
		}
		if len(m.req.Messages) != 1 || m.req.Messages[0].Role != types.RoleUser {
			t.Fatalf("model request %+v, want the assembled history", m.req.Messages)
		}
	})

	t.Run("reply persists to the session log", func(t *testing.T) {
		repliedMessages = nil
		_ = collectEvents(t, "Say hello.")
		if len(repliedMessages) != 1 {
			t.Fatalf("persisted %d replies, want 1", len(repliedMessages))
		}
		if !slices.Contains(repliedMessages, "Hello, quickstart!") {
			t.Fatalf("persisted %v, want the scripted reply", repliedMessages)
		}
	})
}
