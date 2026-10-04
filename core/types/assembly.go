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
