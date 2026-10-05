package main

import (
	"context"
	"strings"
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
		var deltas []string
		var am types.AssistantMessage
		done := false
		for _, ev := range evs {
			switch e := ev.(type) {
			case types.TextDelta:
				deltas = append(deltas, e.Delta)
			case types.AssistantMessage:
				am = e
			case types.Done:
				done = true
			}
		}
		if got := strings.Join(deltas, ""); got != "Hello, quickstart!" {
			t.Fatalf("streamed %q, want %q", got, "Hello, quickstart!")
		}
		if len(am.Message.Blocks) != 1 {
			t.Fatalf("assistant message %v, want one block", am.Message)
		}
		tb, ok := am.Message.Blocks[0].(types.Text)
		if !ok || tb.Text != "Hello, quickstart!" {
			t.Fatalf("assistant block %v, want Text %q", am.Message.Blocks[0], "Hello, quickstart!")
		}
		if !done {
			t.Fatal("no types.Done event")
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

	t.Run("governed send appends the user message", func(t *testing.T) {
		sessionLog = nil
		_ = collectEvents(t, "Say hello.")
		ctx := types.WithPrincipal(context.Background(), types.Principal{
			Tenant:  "local",
			Subject: "reader",
			Scopes:  []string{types.ScopeSessionRead, types.ScopeSessionWrite},
		})
		hist, err := sessionLog.Load(ctx, "quickstart")
		if err != nil {
			t.Fatalf("load session: %v", err)
		}
		if len(hist.Messages) != 1 || hist.Messages[0].Role != types.RoleUser {
			t.Fatalf("session history %+v, want the one user message", hist.Messages)
		}
		tb, ok := hist.Messages[0].Blocks[0].(types.Text)
		if !ok || tb.Text != "Say hello." {
			t.Fatalf("persisted block %v, want Text %q", hist.Messages[0].Blocks[0], "Say hello.")
		}
	})
}
