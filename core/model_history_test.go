package gohan

import (
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func errPartial() types.Message {
	return types.Message{
		ID:     "m-err",
		Role:   types.RoleAssistant,
		Blocks: []types.Block{types.Text{Text: "partial"}},
		Meta:   map[string]any{metaFinish: types.FinishError},
	}
}

func okPartial() types.Message {
	return types.Message{
		ID:     "m-ok",
		Role:   types.RoleAssistant,
		Blocks: []types.Block{types.Text{Text: "done"}},
		Meta:   map[string]any{metaFinish: types.FinishStop},
	}
}

func TestModelTerminalHistory(t *testing.T) {
	t.Run("model.partial-terminal-on-replay", func(t *testing.T) {
		history := []types.Message{
			{ID: "m-u", Role: types.RoleUser},
			errPartial(),
		}
		got := ProviderHistory(history)
		if len(got) != 1 || got[0].ID != "m-u" {
			t.Fatalf("error partial must be excluded, got %d messages", len(got))
		}
	})

	t.Run("completed partial kept", func(t *testing.T) {
		got := ProviderHistory([]types.Message{okPartial()})
		if len(got) != 1 || got[0].ID != "m-ok" {
			t.Fatalf("partial with FinishStop must stay, got %d messages", len(got))
		}
	})

	t.Run("terminal error partial dropped", func(t *testing.T) {
		got := ProviderHistory([]types.Message{errPartial()})
		if len(got) != 0 {
			t.Fatalf("terminal FinishError partial must be dropped, got %d messages", len(got))
		}
	})

	t.Run("unrelated terminal untouched", func(t *testing.T) {
		tool := types.Message{ID: "m-t", Role: types.RoleUser, Meta: map[string]any{metaFinish: types.FinishError}}
		user := types.Message{ID: "m-u", Role: types.RoleUser}
		got := ProviderHistory([]types.Message{tool, user})
		if len(got) != 2 || got[0].ID != "m-t" || got[1].ID != "m-u" {
			t.Fatalf("non-assistant FinishError messages must stay, got %d messages", len(got))
		}
	})

	t.Run("input not mutated", func(t *testing.T) {
		history := []types.Message{{ID: "m-u", Role: types.RoleUser}, errPartial()}
		_ = ProviderHistory(history)
		if len(history) != 2 || history[1].ID != "m-err" {
			t.Fatalf("input slice must not be modified, len %d", len(history))
		}
	})
}
