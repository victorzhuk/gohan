# Design: m2-context-projections

## Context

See `proposal.md` — Why. The behaviour is already normative in `openspec/specs/context/spec.md`; this design decides the seams that connect it to the live tree. Read against the tree on 2026-10-07:

- **One projection exists and nothing calls it.** `std/context.Truncate` implements `Project(_ context.Context, h stores.History, p types.ModelProfile) (stores.History, error)` (`std/context/truncate.go:55`) and is referenced nowhere outside its own package. `stores.History` is `{Owner, Messages, Version, ForkedFrom}` (`core/stores/session.go:12-17`).
- **The assembler seam is already the right place.** `nativeAssemble` composes the caller assembler with blob resolution, the redactor and the operator-turn fence (`core/native_effects.go:215-243`), and `assemblyInput` hands it the loaded history through `types.AssembleInput{History, Profile}` (`core/native_effects.go:202-209`; `core/types/assembly.go:25-36`). The fence, the blob step and redaction all run *after* `assemble`, so projections must run before it.
- **Counting is already a shipped port.** `ContextBudget(p, req, est, ratio)` derives the ceiling and consults a `TokenEstimator` exactly at the decision (`core/types/context_budget.go:16-33`); `TokenCounter` is the optional exact-count port (`core/types/tokens.go:19`). `Truncate` already mirrors both through `WithTruncateEstimator` and `WithTruncateRatio` (`std/context/truncate.go:20-42`).
- **Adjacency is a live invariant.** `droppable` in `std/context` already refuses to split a `ToolUse` from its result (`std/context/truncate.go`, `droppable`); `core` keeps the journal's own arguments, so the journal is not a view over the model-facing history.
- **Reserved names already exist.** `notes_read` and `notes_write` are in `reservedToolNames` (`core/tool_names.go:17-28`), so exclusion is decidable by tool name without a new port.
- **No scenario test exists.** `std/context/truncate_test.go` and `std/context/truncate_notes_test.go` cover `Truncate` only; nothing names the five owned ids.

## Goals / Non-Goals

**Goals:** declare the projection stage and its policy, run it in declared order before the caller assembler inside the existing wrapper, implement `ClearToolResults` with the retention, exclusion and batched-firing rules, and turn the five owned ids green on the public path for `Build`, `Send` and `Resume`.

**Non-Goals:** This change persists nothing. It does not add the `Compactor` stage, `Compaction` blocks, `CoversUpTo` hiding, `Summarize`, `ProviderCompact`, calibration ratios, the overflow retry or the `Compacted` audit record — those belong to `m2-context-persisted-compaction`, `m2-provider-compaction` and `m2-context-calibration-overflow` (`openspec/changes/m2/design.md:53-73`). It does not change `Truncate`, the taint window, the journal, the blob step or the redaction step. It adds no new store, no new metric and no new error sentinel: the cleared-token metric `gohan.context.tokens_cleared` is named by the capability spec but is emitted by the budget-owning stage and is not claimed here (`openspec/specs/context/spec.md`, Metrics).

## Decisions

### D1 — The stage runs inside `nativeAssemble`, before the caller assembler

`nativeAssemble` is the single point where the native path turns loaded history into a model request, and it is already a composition of ordered stages (caller assembler → blobs → redaction → operator-turn fence → prompt block). The projection stage is prepended, not inserted: `nativeAssemble` gains a `[]stores.Projection` and a `stores.History` (built from the loaded messages with the run's history version) and applies the projections to a copy before calling `assemble`. Rationale: the caller assembler owns prefix layout and providers, so the projected messages must already be inside `AssembleInput.History` — a projection applied afterwards would reach a request the assembler has already shaped, and the `Truncate` sibling works on `stores.History`, not on `ModelRequest`. Keeping the stage ahead of the caller assembler is what the delta's first paragraph requires, and it leaves blob resolution, redaction and the operator-turn fence exactly where they are: after the caller assembler. A flow with an empty projection list runs today's code path unchanged.

### D2 — `Projection` and `ContextPolicy` are declared in `core/stores`, beside `History`; `std/context` holds the implementations

The seam is declared in `core/stores`, in the package that already owns `History`. The reason is the import edge: `core/stores` imports `core/types` (`core/stores/session.go:8`), so a `Projection` interface placed in `core/types` cannot name `stores.History` in its method set — `core/types` would have to import `core/stores` and the two packages would form a cycle, so the declaration does not compile. The alternative, moving `History` down into the floor package, is a wider migration than this change needs: it would move a shipped stored shape out of the package that owns session history and force every holder of it to change import, which belongs to the freeze work, not to the projection stage. `core/stores` is inside the `core` package tree, so the port stays a capability port and not a leaf-package detail, and `core/stores` already sees both `types` and `History`, so `Project` can name `types.ModelProfile` and `stores.History` without a second import decision. The interface is the shipped `Truncate` shape, so `Truncate` satisfies it with no signature change: `Project(ctx context.Context, h History, p types.ModelProfile) (History, error)`. `ContextPolicy{Projections []Projection; Compactor Compactor}` is the one shape that names both stages; this change declares the `Compactor` field so the type does not change at the v1 freeze, and leaves it nil — a later change fills it without an edit to a frozen struct. `std/context` keeps both implementations, because `Truncate`'s turn grouping, pending-call rule and estimator options already live there and `ClearToolResults` shares that grouping. No second copy of either rule is added to core.

### D3 — `ClearToolResults` matches by `ToolUse.ID` and counts the last `Keep` tool uses in history order

A `ToolResult` carries an `ID` and no tool name (`core/types/message.go:142-149`), so the tool is found by scanning history for the `ToolUse` with the matching `ID` (`core/types/message.go:136-141`). Counting is a single forward pass that records every `ToolUse.ID` in history order, whether or not a `ToolResult` for it exists yet; the retained window is the last `Keep` tool uses of that list, and only the results of uses outside the window are candidates for clearing. A pending `ToolUse` — one whose result is not in history — still occupies a slot in the window, because its use is still a tool use in history order, but it has no result to clear and therefore never becomes a candidate. Every `ToolUse` block stays in place and every `ToolResult` stays in its own message position, so adjacency survives a clearing. Exclusion is by the resolved tool name, and `notes_read`/`notes_write` are excluded regardless of what the caller's `Exclude` lists, so a misconfigured policy cannot clear the session notes the harness relies on.

### D4 — The placeholder is fixed, and the result keeps its identity

A cleared result keeps `ID`, `Outcome`, `Error`, `Ref` and every block's `Origin`, and its content is replaced by one deterministic text placeholder. The placeholder is a package constant, not a formatted string: determinism is a scenario (`context.projection-deterministic`), and a constant keeps the cleared-token arithmetic and the request bytes stable across processes. Identity and outcome survive so the model still sees which call returned what status, and the origin survives so taint and redaction reasoning downstream sees the same provenance the source block carried.

### D5 — `ClearAtLeast` is decided through `ContextBudget`, and a firing is all-or-nothing

The projection estimates the original request through the declared estimator, builds the candidate with the clearing applied, and takes the difference. The value is defined as the positive difference between the two estimates, placeholder cost included, so a clearing that removes content but adds a large placeholder is not credited for the loss. If the difference is below `ClearAtLeast`, the projection returns the input history unchanged — no partial clearing, no partial write of state. This is what makes the cache invalidation batched instead of per turn, and it is why the decision runs through the same `ContextBudget` shape `Truncate` uses (`core/types/context_budget.go:16-33`): one estimator, one counting rule, one place where the ratio enters. Without an estimator the projection never fires and returns the history unchanged, matching `Truncate`'s rule (`std/context/truncate.go:20-26`). Whether the ratio is calibrated from usage is the later change's concern; this one takes the ratio as an option.

## Invariants the later persisted-compaction change must not break

These hold now and are restated here because `m2-context-persisted-compaction` runs the stage that can break them:

1. **Pending calls survive.** A `ToolUse` whose `ToolResult` is not in `SessionLog` (pending, suspended for approval, `Outcome: Unknown`) holds its own `ToolUse` block and is never cleared and never covered. It still occupies a slot in the `Keep` window (D3), because the window counts tool uses in history order, and its absent result is never treated as cleared content.
2. **Tool-use/result adjacency.** A `ToolUse` and its `ToolResult` are covered together or not at all; the result's position in history never changes.
3. **The taint window is the full log.** Taint is computed over the full `SessionLog` window, never over the projection — clearing a block does not untaint a value copied from it (`core/native_effects.go:85-92` builds the raw window from the run messages).
4. **Journal fingerprints are unaffected.** Loop detection and idempotency work over `Journal`, not over what the model can see; a projection cannot change a fingerprint.

## Shape to settle before the v1 freeze

M2 freezes every port that exists at its exit (`docs/overview.md:108`), so this change settles the following before it closes:

- `Projection.Project(ctx, History, types.ModelProfile) (History, error)`, declared in `core/stores` — the method set, including whether `Projection` stays an interface or becomes a struct with a function field. It stays an interface: `Truncate` already satisfies it and callers declare heterogeneous stages.
- `ContextPolicy` field set — `Projections` and `Compactor`, both declared in `core/stores`, so the later compaction change adds no method and no field to a frozen type.
- `ClearToolResults{Keep, ClearAtLeast, Exclude}` as a value struct in `std/context` with option setters for the estimator and the ratio, matching `Truncate`'s `With…` shape.
- The deterministic placeholder text, once `std/context` and the frozen `docs/design/types.md` index record it.

## Risks / Trade-offs

- [Risk] A projection silently becomes a write. A projection that persists its result would break `context.projection-does-not-write` while still looking correct in assembly, and the later compaction stage gives it a natural excuse to write. → Mitigation: the seam passes `stores.History` by value and nothing else — no store handle, no session id and no run id reach a projection — so there is no store to write to. The public-path test asserts `SessionLog.Load` returns the full results and the version is unchanged after a firing, and the same test is re-run by `m2-context-persisted-compaction`.
- [Risk] Clearing in place would corrupt the cached prefix and the history the next turn loads. → Mitigation: every clearing copies the message slice and replaces blocks in the copy; the `Truncate` sibling already returns a new history with `Version` carried over.
- [Risk] Batch clearing removes content the model still needs, because `Keep` counts tool uses rather than turns, and a pending use occupies a window slot without a result to keep. → Accepted and stated: `Keep` is the contract's unit, the retained window is the last `Keep` tool uses in history order, a pending use keeps its own `ToolUse` block and has nothing to lose, and the excluded tools are never cleared at all.
- [Risk] Clearing changes the operator-turn fence's output, because the fence runs after the assembler over messages the projection already reduced. → Accepted: the fence keys on block origin (D4 of `m1-session-metadata-handoff`), and a placeholder carries the origin of the block it replaced, so operator turns inside cleared results stay fenced.
- [Risk] The estimator is optional, so a flow without one never clears and grows without bound. → Accepted: it matches `Truncate`'s shipped rule; the flow opts in by declaring an estimator, and the projection's own tests always declare one.

## Migration Plan

No data migration is required. The change adds the `Projection` and `ContextPolicy` declarations, the option that binds a policy, one stage inside `nativeAssemble`, and one new projection in `std/context`. Flows that declare no policy keep the current request bytes. No stored shape and no event type changes, so `docs/design/types.md` is regenerated for the new declarations only, and no version is cut by this change.