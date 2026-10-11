package types

import (
	"context"
	"testing"
)

func TestForeignCallIdentityCheck(t *testing.T) {
	t.Run("carried-through-derived-context", func(t *testing.T) {
		call := ToolUse{ID: "call-1", Name: "confirm", Args: []byte(`{"n":1}`)}
		ctx := WithToolUse(context.Background(), call)
		derived := WithRunInfo(ctx, RunInfo{RunID: "r1"})
		got, ok := ToolUseFrom(derived)
		if !ok {
			t.Fatal("ToolUseFrom: ok = false, want the carried call")
		}
		if got.ID != "call-1" || got.Name != "confirm" || string(got.Args) != `{"n":1}` {
			t.Fatalf("ToolUseFrom: got %+v, want the carried call", got)
		}
	})
	t.Run("absent-outside-a-carried-call", func(t *testing.T) {
		if _, ok := ToolUseFrom(context.Background()); ok {
			t.Fatal("ToolUseFrom: ok = true outside a carried call, want false")
		}
		empty := WithToolUse(context.Background(), ToolUse{})
		got, ok := ToolUseFrom(empty)
		if !ok || got.ID != "" {
			t.Fatalf("ToolUseFrom: got (%+v, %v), want zero call, true", got, ok)
		}
	})
}
