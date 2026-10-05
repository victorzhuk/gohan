package gohan_test

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
)

type slotProvider struct {
	slot   types.ContextSlot
	blocks []types.Block
}

func (p slotProvider) Slot() types.ContextSlot { return p.slot }

func (p slotProvider) Provide(context.Context, types.RunInfo) ([]types.Block, error) {
	return p.blocks, nil
}

func prefixInput() std.AssembleInput {
	return std.AssembleInput{
		Run: types.RunInfo{
			Flow:      "bench",
			SessionID: "s1",
			RunID:     "r1",
			Turn:      2,
			Principal: types.Principal{Subject: "u", Tenant: "t", Scopes: []string{"session:read"}},
			CostTags:  types.CostTags{Feature: "f", Environment: "e", CostCenter: "c"},
		},
		System: []types.Block{types.Text{Text: "system"}},
		Tools:  []types.ToolSpec{{Name: "bravo"}, {Name: "alpha"}},
		History: []types.Message{
			{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "earlier"}}},
		},
		Input: []types.Message{
			{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "question"}}},
		},
		Providers: map[types.ContextSlot][]types.ContextProvider{
			types.SlotStatic:  {slotProvider{slot: types.SlotStatic, blocks: []types.Block{types.Text{Text: "static"}}}},
			types.SlotSession: {slotProvider{slot: types.SlotSession, blocks: []types.Block{types.Text{Text: "session"}}}},
			types.SlotTurn:    {slotProvider{slot: types.SlotTurn, blocks: []types.Block{types.Text{Text: "turn"}}}},
		},
	}
}

func TestPrefixBuildAllocationFree(t *testing.T) {
	t.Run("performance.prefix-build-allocation-free", func(t *testing.T) {
		ctx := context.Background()
		in := prefixInput()
		first, err := (std.StablePrefix{}).Assemble(ctx, in)
		if err != nil {
			t.Fatalf("first Assemble: %v", err)
		}
		second, err := (std.StablePrefix{}).Assemble(ctx, in)
		if err != nil {
			t.Fatalf("second Assemble: %v", err)
		}
		if len(first.System) != len(second.System) || len(first.Messages) != len(second.Messages) || len(first.Tools) != len(second.Tools) {
			t.Fatalf("rebuild drifted: system %d/%d messages %d/%d tools %d/%d",
				len(first.System), len(second.System),
				len(first.Messages), len(second.Messages),
				len(first.Tools), len(second.Tools))
		}
		if first.Tools[0].Name != "alpha" || first.Tools[1].Name != "bravo" {
			t.Fatalf("tools not sorted by name: %q %q", first.Tools[0].Name, first.Tools[1].Name)
		}
		avg := testing.AllocsPerRun(100, func() {
			req, err := (std.StablePrefix{}).Assemble(ctx, in)
			if err != nil {
				t.Errorf("repeat Assemble: %v", err)
			}
			if len(req.Messages) == 0 {
				t.Error("repeat Assemble lost the input messages")
			}
		})
		if avg != 0 {
			t.Fatalf("repeat prefix build = %g allocs/run, want 0", avg)
		}
	})
}

func BenchmarkPrefixBuild(b *testing.B) {
	ctx := context.Background()
	in := prefixInput()
	if _, err := (std.StablePrefix{}).Assemble(ctx, in); err != nil {
		b.Fatalf("Assemble: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := (std.StablePrefix{}).Assemble(ctx, in); err != nil {
			b.Fatalf("Assemble: %v", err)
		}
	}
}
