# Context assembly

Capability: `assembly` · Spec v1.0 baseline (restructured from gohan-spec v0.13; later decisions live in `docs/adr/`) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `assembly` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.11 Context assembly

```go
type ContextSlot int

const (
	SlotStatic ContextSlot = iota
	SlotSession
	SlotTurn
)

type ContextProvider interface {
	Slot() ContextSlot
	Provide(ctx context.Context, ri RunInfo) ([]Block, error)
}

type AssembleInput struct {
	Run       RunInfo
	Profile   ModelProfile
	System    []Block
	Tools     []ToolSpec
	History   History
	Input     []Message
	Providers map[ContextSlot][]ContextProvider
}

type Assembler interface {
	Assemble(ctx context.Context, in AssembleInput) (ModelRequest, error)
}

type ToolFilter func(ctx context.Context, ri RunInfo, specs []ToolSpec) []ToolSpec

```

Canonical layout (prefix-stable):

```
system instruction
tool specs (sorted by name, byte-stable JSON)
SlotStatic providers (skill catalog, …)   ── CacheBreak
SlotSession providers (memory, user profile)   ── CacheBreak
history
SlotTurn providers (retrieved docs, per-turn facts)
new input
```

`ToolFilter` runs per turn before assembly and may narrow (never widen) the registered tools by business state (e.g. hide `create_booking` until a slot is selected). Filtered-out tools stay governed; a model call to one is an unknown-tool error.

Rules: no timestamps, run IDs or random values before the last `CacheBreak`; providers must be deterministic for identical inputs. `ContextPolicy` (projections + compactor) is defined by the `context` capability; projections run before `Assemble`, a persisted `Compaction` block replaces the history it covers. Truncation is the `Truncate` projection of `context`, so assembly never reduces `SessionLog`; the only persisted history reduction is a `Compaction` block.


## Requirements

### Requirement: Context assembly

#### Scenario: prefix stability
ID: `assembly.prefix-stability`
- WHEN the same flow runs twice for different users with the same static providers
- THEN the byte prefix up to the first `CacheBreak` is identical

#### Scenario: tool order
ID: `assembly.tool-order`
- WHEN tools are registered in different orders
- THEN assembled tool specs are identical

#### Scenario: truncate policy
ID: `assembly.truncate-policy`
- WHEN history exceeds `TokenBudget.Limit` (`model`)
- THEN the `Truncate` projection (`context`) drops the oldest turns whole (never splitting a tool call from its result) before `Assemble`, the prefix is unchanged and `SessionLog` keeps every turn
