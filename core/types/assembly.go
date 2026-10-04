package types

import "context"

// ContextSlot names where a provider's blocks land in the assembled
// request. The slot order fixes the cache boundary: everything before the
// last CacheBreak must be byte-stable across runs.
type ContextSlot int

const (
	SlotStatic ContextSlot = iota
	SlotSession
	SlotTurn
)

// ContextProvider contributes blocks to one slot. It must be deterministic
// for identical inputs: no timestamps, run IDs or random values in any slot
// that precedes the last CacheBreak.
type ContextProvider interface {
	Slot() ContextSlot
	Provide(ctx context.Context, ri RunInfo) ([]Block, error)
}

// AssembleInput carries everything one assembly reads. It mirrors the
// assembly contract: the providers map is keyed by slot, the loaded history
// messages arrive as a slice, and assembly itself never touches a store.
// The Filter field keeps an unnamed type so the std package can keep its
// named ToolFilter without a floor dependency on it.
type AssembleInput struct {
	Run       RunInfo
	Profile   ModelProfile
	System    []Block
	Tools     []ToolSpec
	History   []Message
	Input     []Message
	Providers map[ContextSlot][]ContextProvider
	Filter    func(specs []ToolSpec, turn int) []ToolSpec
}
