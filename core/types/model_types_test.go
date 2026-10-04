package types

import (
	"encoding/json/v2"
	"reflect"
	"testing"
)

func TestModelTypes(t *testing.T) {
	t.Run("messages.typed-deltas", func(t *testing.T) {
		chunks := []ModelChunk{
			{Kind: DeltaReasoning, Delta: "thinking"},
			{Kind: DeltaReasoning, Delta: " more"},
			{Kind: DeltaText, Delta: "answer"},
			{Kind: DeltaText, Delta: " done"},
		}
		if chunks[0].Kind != DeltaReasoning || chunks[1].Kind != DeltaReasoning {
			t.Fatalf("leading chunks must be DeltaReasoning, got %v and %v", chunks[0].Kind, chunks[1].Kind)
		}
		for i, c := range chunks[2:] {
			if c.Kind != DeltaText {
				t.Fatalf("chunk %d after reasoning must be DeltaText, got %v", i+2, c.Kind)
			}
		}
		if DeltaToolArgs == DeltaText || DeltaToolArgs == DeltaReasoning {
			t.Fatalf("DeltaToolArgs must be a distinct kind, got %d", DeltaToolArgs)
		}
		if DeltaText != 0 || DeltaReasoning != 1 || DeltaToolArgs != 2 {
			t.Fatalf("DeltaKind const values must stay stable, got %d %d %d", DeltaText, DeltaReasoning, DeltaToolArgs)
		}

		for r, s := range map[FinishReason]string{
			FinishStop:      "stop",
			FinishToolUse:   "tool_use",
			FinishMaxTokens: "max_tokens",
			FinishRefusal:   "refusal",
			FinishError:     "error",
		} {
			if string(r) != s {
				t.Fatalf("FinishReason %q must stay %q", string(r), s)
			}
		}

		in := Usage{
			InputTokens:       12,
			CachedInputTokens: 4,
			OutputTokens:      34,
			CacheWriteTokens:  2,
			SandboxSeconds:    1.5,
			ProviderToolCalls: map[string]int{"web": 3},
			KeyID:             "k1",
			ModelVersion:      "m-9",
			Estimated:         true,
		}
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var out Usage
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !reflect.DeepEqual(out, in) {
			t.Fatalf("round-trip mismatch:\nin  %+v\nout %+v", in, out)
		}
		if out.ProviderToolCalls["web"] != 3 {
			t.Fatalf("ProviderToolCalls lost in round-trip: %v", out.ProviderToolCalls)
		}
		if out.SandboxSeconds != 1.5 {
			t.Fatalf("SandboxSeconds lost in round-trip: %v", out.SandboxSeconds)
		}
	})
}
