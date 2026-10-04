package std

import (
	"context"
	"fmt"
	"sort"

	"github.com/victorzhuk/gohan/core/types"
)

// AssembleInput carries everything one StablePrefix assembly reads. It
// mirrors the assembly contract: the providers map is keyed by slot and the
// history arrives already loaded, so assembly itself never touches a store.
type AssembleInput = types.AssembleInput

// StablePrefix assembles a request whose byte prefix up to the last
// CacheBreak is identical across runs that differ only after the boundary:
// no timestamps, run IDs or random values precede it, and tools are emitted
// sorted by name.
type StablePrefix struct{}

// Assemble builds the request in the canonical order: system instruction,
// tool specs sorted by name, static providers, a CacheBreak, session
// providers, a second CacheBreak, history, turn providers, new input.
func (StablePrefix) Assemble(ctx context.Context, in AssembleInput) (types.ModelRequest, error) {
	tools, err := NarrowTools(in.Tools, in.Filter, in.Run.Turn)
	if err != nil {
		return types.ModelRequest{}, fmt.Errorf("narrow tools: %w", err)
	}
	sorted := make([]types.ToolSpec, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	system := make([]types.Block, 0, len(in.System)+4)
	system = append(system, in.System...)
	system, err = appendSlot(ctx, system, in, types.SlotStatic, in.Run)
	if err != nil {
		return types.ModelRequest{}, err
	}
	system = append(system, types.CacheBreak{})
	system, err = appendSlot(ctx, system, in, types.SlotSession, in.Run)
	if err != nil {
		return types.ModelRequest{}, err
	}
	system = append(system, types.CacheBreak{})

	messages := make([]types.Message, 0, len(in.History)+len(in.Input)+1)
	messages = append(messages, in.History...)
	turn, err := provideSlot(ctx, in, types.SlotTurn, in.Run)
	if err != nil {
		return types.ModelRequest{}, err
	}
	if len(turn) > 0 {
		messages = append(messages, types.Message{Role: types.RoleUser, Blocks: turn})
	}
	messages = append(messages, in.Input...)

	return types.ModelRequest{System: system, Tools: sorted, Messages: messages}, nil
}

func appendSlot(ctx context.Context, system []types.Block, in AssembleInput, slot types.ContextSlot, ri types.RunInfo) ([]types.Block, error) {
	blocks, err := provideSlot(ctx, in, slot, ri)
	if err != nil {
		return system, err
	}
	return append(system, blocks...), nil
}

func provideSlot(ctx context.Context, in AssembleInput, slot types.ContextSlot, ri types.RunInfo) ([]types.Block, error) {
	var blocks []types.Block
	for _, p := range in.Providers[slot] {
		got, err := p.Provide(ctx, ri)
		if err != nil {
			return nil, fmt.Errorf("provide slot %d: %w", slot, err)
		}
		blocks = append(blocks, got...)
	}
	return blocks, nil
}
