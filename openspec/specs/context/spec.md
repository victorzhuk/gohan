# Context management

Capability: `context` · Spec v1.2 (ADR-0118) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

Keeps a long-running session inside the model's context window without losing the properties the rest of the harness depends on: `SessionLog` stays the single source of truth, `Replay` stays deterministic, the audit trail stays complete, and provider differences in compaction stay behind one capability flag. Out of scope: cross-session memory (`working-state`), retrieval (`ContextProvider` in `assembly`), the prefix layout (`assembly`).


## Contract

### Two stages with different persistence

```go
type ContextPolicy struct {
	Projections []Projection
	Compactor   Compactor
}

type Projection interface {
	Project(ctx context.Context, h History, p ModelProfile) (History, error)
}

type Compactor interface {
	Compact(ctx context.Context, h History, p ModelProfile) (Compaction, error)
}

// Compaction block: defined in `messages`

type CompactionKind int

const (
	CompactionText CompactionKind = iota + 1
	CompactionOpaque
)

type ClearToolResults struct {
	Keep         int
	ClearAtLeast int
	Exclude      []string
}
```

**Projection** is an assembly-time view over `History`. It is deterministic for identical `(History.Version, ModelProfile)`, is applied in declared order before `Assembler.Assemble`, and is never written to `SessionLog`. Implementations in `std/context`: `Truncate` (drop oldest turns), `ClearToolResults` (replace `ToolResult.Content` older than the last `Keep` tool uses with a placeholder and keep the `ToolUse`; clears at least `ClearAtLeast` tokens per firing so the cache prefix is invalidated in batches, not per turn; tools in `Exclude`, always including `notes_read`/`notes_write`, are never cleared).

**Compaction** is persisted. `Compactor.Compact` runs when the assembled request would overflow `ModelProfile.ContextWindow` after projections, or when `ContextPolicy` declares a token trigger; the result is appended to `SessionLog` as a `Compaction` block (a `Message` with `Role: RoleSystem` and a single block) whose `CoversUpTo` names the last history version it replaces. Assembly hides every message with version ≤ `CoversUpTo` and renders the `Compaction` block in their place. Implementations: `Summarize` (harness-side, instruction from `PromptSet.Compaction`, produces `CompactionText`) and `ProviderCompact` (adapter-side, produces `CompactionText` or `CompactionOpaque` according to `Caps.Compaction`).

Rules:

1. A `ToolUse` whose `ToolResult` is not yet in `SessionLog` (pending, suspended for approval, or `Outcome: Unknown`) is never covered by a compaction; `CoversUpTo` is clamped below it.
2. A `ToolUse`/`ToolResult` pair is covered together or not at all; a projection never clears a `ToolResult` whose `ToolUse` is inside the last `Keep`.
3. `CompactionText` carries `Origin{Kind: OriginModel}` and passes `StageContext` guards before it is appended; a rejected summary is not persisted and the turn falls back to `Truncate`.
4. `CompactionOpaque` is bound to `Compaction.Model`; assembly for a different endpoint drops it and re-fits with projections and `Summarize`. `Build` rejects `ProviderCompact` on a profile with `Caps.Compaction == CompactionOpaqueCap` when the flow's `Fallback` crosses providers.
5. `Replay` reconstructs the same view: the persisted `Compaction` block is part of history, so a resumed run assembles exactly what the original run assembled.
6. `AuditLog` receives a `Compacted` record (from/to version, tokens before/after, policy name, summary checksum); `EventLog` receives the `Compacted` event.
7. **Counting and calibration.** Overflow and every token-denominated trigger (`ClearAtLeast`, `ContextPolicy`) are decided by `ContextBudget` (`model`). After each response the harness sets `State.Calibration[profile] = Usage.InputTokens / Estimated`, clamped to [0.5, 2.0]; the ratio in force is recorded in the `Compacted` audit record and the checkpoint, so `Replay` reaches the same decision. When the model implements `TokenCounter`, the compaction decision uses the exact count instead.
8. **Overflow backstop.** A `ClassContextOverflow` on a request the budget accepted forces one `Compact` with the ratio raised to the provider's reported overflow factor (or ×1.25), then one retry; a second overflow fails the turn `Permanent`. `gohan.context.overflow_retry{profile}` counts retries and `gohan.context.estimate_error{profile}` records `|ratio − 1|` on every response.
9. Taint (`taint`) is computed over the full `SessionLog` window, never the projection: clearing or compacting a block does not untaint values copied from it.
10. Journal fingerprints are unaffected: loop detection and idempotency work over `Journal`, not over what the model can see (`stores.fingerprint-after-compaction`).

Metrics (`telemetry`): `gohan.context.compactions` counter by kind, `gohan.context.tokens_cleared` counter, `gohan.context.tokens_before`/`tokens_after` histograms, `gohan.context.cache_invalidated` counter.


## Requirements

### Requirement: Projections are views

#### Scenario: session log untouched
ID: `context.projection-does-not-write`
- WHEN `ClearToolResults` clears three old results during assembly
- THEN `SessionLog.Load` still returns the full results and `History.Version` is unchanged

#### Scenario: deterministic projection
ID: `context.projection-deterministic`
- WHEN the same `History.Version` is assembled twice for the same profile
- THEN the projected request bytes are identical

#### Scenario: keep last N
ID: `context.keep-last-n`
- WHEN `ClearToolResults{Keep: 3}` runs over a history with six completed tool calls
- THEN the three most recent results are intact, the three older ones are placeholders, and all six `ToolUse` blocks are present

#### Scenario: notes never cleared
ID: `context.notes-excluded`
- WHEN `notes_read` results are older than the last `Keep` tool uses
- THEN they are not cleared

#### Scenario: batched clearing
ID: `context.clear-at-least`
- WHEN clearing is triggered with `ClearAtLeast: 15000`
- THEN at least 15 000 tokens are cleared in one firing or none at all

### Requirement: Counting and calibration

#### Scenario: calibration from usage
ID: `context.calibration-from-usage`
- WHEN a response reports 12 000 input tokens for a request estimated at 10 000
- THEN the next estimate for the same profile is multiplied by 1.2

#### Scenario: calibration persisted for replay
ID: `context.calibration-persisted-for-replay`
- WHEN a run compacted with ratio 1.2 is replayed on another pod
- THEN the replay reads the ratio from the checkpoint and reaches the same compaction decision

#### Scenario: overflow compacts and retries once
ID: `context.overflow-compacts-and-retries-once`
- WHEN the provider returns `ClassContextOverflow` for a request the budget accepted
- THEN one compaction runs with the raised ratio, the request is retried once, and a second overflow fails `Permanent` with `gohan.context.overflow_retry` at 1

### Requirement: Compaction is persisted

#### Scenario: compaction appended
ID: `context.compaction-persisted`
- WHEN `Summarize` compacts a history at version 40
- THEN `SessionLog` holds a `Compaction` block with `CoversUpTo: 40` and the next assembly hides versions ≤ 40

#### Scenario: replay after compaction
ID: `context.replay-after-compaction`
- WHEN a run compacts, suspends and is resumed with `Replay` on another pod
- THEN the resumed run assembles the same request bytes as the original run did after compaction

#### Scenario: pending call survives
ID: `context.pending-call-survives`
- WHEN compaction is triggered while a `ToolUse` awaits `HumanApproval`
- THEN `CoversUpTo` is below that `ToolUse` and the resumed run still sees it

#### Scenario: summary is guarded
ID: `context.summary-guarded`
- WHEN a harness-side summary contains an instruction the `StageContext` guard rejects
- THEN nothing is persisted, `Truncate` is used for the turn and `gohan.guard.context_rejected{provider="compaction"}` increments

#### Scenario: opaque compaction bound to model
ID: `context.opaque-bound-to-model`
- WHEN a `CompactionOpaque` block exists and the router selects a different endpoint
- THEN the block is dropped from assembly and the request is re-fitted with projections and `Summarize`

#### Scenario: opaque under cross-provider fallback rejected
ID: `context.opaque-fallback-rejected`
- WHEN a flow uses `ProviderCompact` on a profile with `Caps.Compaction == CompactionOpaqueCap` and its `Fallback` lists another provider
- THEN `Build` returns an error naming flow, profile and strategy

#### Scenario: compaction audited
ID: `context.compaction-audited`
- WHEN any compaction is persisted
- THEN `AuditLog` holds a `Compacted` record with versions, token counts, policy and summary checksum, and `EventLog` holds the `Compacted` event
