# Design

## Context

The `working-state` subject-memory Contract (`openspec/specs/working-state/spec.md:56-78`) already names the target surface; the implementation is the gap. Verified this session:

- `stores.MemoryStore` and its three methods ship at `core/stores/optional.go:23`; the reference store implements them at `core/stores/optional.go:37-70`. `MemoryEntry` carries `ID`, `Value`, `Origins`, `RunID`, `Version` and **no timestamp** (`core/stores/optional.go:11-17`).
- `ErrMemoryStoreRequired` ships at `core/types/errors.go:63`, and `memory_read`/`memory_write` are already reserved tool names at `core/tool_names.go:22-23`.
- Nothing constructs the pair: `std/notes` ships `New` (session notes), `WithMaxNotes`, `WithRedaction` and the `Notes` slot provider (`std/notes/notes.go:56,38,31,138,142` — `WithRedaction` at `:31`, `WithMaxNotes`
at `:38`, `:115` is the private `sessionKey`) — there is no `Memory`, no `MemoryPolicy`, no `AllowUntrustedMemory`, and no `Stack.Memory`/`Stack.ForgetMemory` (`grep` over `core/*.go` finds neither; the only `Stack` readers of memory are `EraseSubject` at `core/erasure.go:27`).
- Erasure already reads the `MemoryEraser` seam (`core/stores/erasure.go:55`, dispatched at `core/erasure.go:72`) and is not part of this change.
- The untrusted-turn guard is `std/guard`'s `ContextGuard`, run at stage `StageContext` (`openspec/specs/guards/spec.md:67`, `std/guard/context_test.go:17`); taint rule 6 is settled at `openspec/specs/taint/spec.md:52`.

Milestone handoff: `openspec/changes/m1/design.md:107-113` assigns all seven scenarios to this change; six are `integration gap` and `working-state.memory-ttl-eviction` is `missing` because `MemoryEntry` has no timestamp. §3 of that file seals the decisions this design builds on: ownership split (D2), exfil derivation owner (D5), permission order (D6).

## Goals / Non-Goals

**Goals:**
- Put the seven deferred scenarios on the public path: an isolated, schema-validated, origin-carrying, TTL-bounded subject memory reachable through `memory_write`/`memory_read`, `Stack.Memory` and `Stack.ForgetMemory`.
- Add the store contract the scenarios require: a store-clock timestamp and store-side eviction.
- Close `working-state.memory-ttl-eviction`, the one `missing` row.

**Non-Goals:**
- Subject erasure (`redaction`/`m1-subject-erasure-residency` already covers it; the `MemoryEraser` seam exists).
- Ambient injection of memory into context — subject memory never enters through a `SlotSession` provider (working-state rule 2).
- The `languages` `memory:` declaration syntax; this change consumes the policy value, not the flow-definition grammar.

## Decisions

**D1 — Delta is `## MODIFIED Requirements` on the existing *Subject memory* requirement; all eight scenarios stay verbatim.** The scenarios are registry-bound (`openspec/scenarios.json:1986-2028` for the seven, `:2150` for `memory-store-optional`), the WHEN/THEN lines are what a binding script matches, and the main requirement block holds no description prose at all. The normative additions therefore go into a new requirement description (schema validation, store-clock stamp, eviction) plus the Contract struct fields `MemoryEntry.StoredAt` and `MemoryPolicy`; the scenarios are copied unchanged. Alternative — an `## ADDED Requirements` block with the same scenarios — was rejected: it would duplicate registry-bound IDs and unbind the tests.

**D2 — the store validates and evicts; the tool only shapes the call.** `MemoryStore.Put` (`core/stores/optional.go:50`) grows schema validation against the policy and eviction of expired/oldest entries. Alternative — validate in `memory_write` only — was rejected: a second store implementation would silently skip the rule, and the erasure/consolidation writers reach the store directly. The store owns the invariant; the tool owns the denial path.

**D3 — the store stamps `StoredAt` from its own clock; callers pass no instant.** This follows the two-clocks rule (`openspec/specs/stores/spec.md:333`): persisted, later-compared instants come from the store clock, so a skewed pod cannot shorten a TTL. `MemoryEntry` gains `StoredAt time.Time`; `MemoryNotes` sets it, a durable store persists it in its own column.

**D4 — `notes.Memory(store, policy, opts...)` registers `memory_write`/`memory_read` and `Build` resolves it like the notes pair.** `Build` fails `ErrMemoryStoreRequired` when the store does not implement `MemoryStore` (`working-state.memory-store-optional`). Both tools are `ReadOnly` effect; `memory_write` is `Trusted` with `Risk: High`, matching working-state rule 2. The pair is never a `SlotSession` provider.

**D5 — untrusted-turn denial sits at the `StageContext` guard.** `memory_write` input passes the existing context guard (`openspec/specs/guards/spec.md:67`); when the taint window contains an `Untrusted` block and the flow declared no `notes.AllowUntrustedMemory()`, the write returns `Failed(Permanent)` and counts `gohan.memory.write_denied`. This is the D6 permission order of `openspec/changes/m1/design.md` §3 applied to a tool whose effect is `ReadOnly`; the change depends on `m1-taint-enforcement`, as the milestone records.

**D6 — the read path carries the entry's stored origins.** `memory_read` builds result blocks as `OriginTool{"memory_read"}` with each entry's stored `Origins` attached, so taint rule 6 applies and an `Exfil` call made with a recalled value takes the default deny (`openspec/specs/taint/spec.md:52`). Origins are captured from the writing turn's blocks at write time, not re-derived on read.

**D7 — consolidation is exactly one `std/flow.Extract` at `Done`.** The session-to-subject summarisation is `std/flow.Extract` (`std/flow/extract.go:45`) over `SessionLog` with the same `Schema`, run once at `Done` and audited `memory_consolidate` naming the source session (working-state rule 4, `openspec/specs/working-state/spec.md:73`). Compaction never writes subject memory.

**D8 — controls are owner-checked.** `Stack.Memory(ctx, owner)` and `Stack.ForgetMemory(ctx, owner, ids...)` refuse a caller that does not own the `SessionOwner`, using the same check as `Inspect` (`core/inspect.go:35`), and build the `ScopeSubject` key from the owner rather than from any tool argument.

**D9 — the eviction metric's `tenant` label needs `WithTenantLabel()`.** `gohan.memory.evicted` is emitted on eviction; `tenant` is on the telemetry allow-list only behind `WithTenantLabel()` (`openspec/specs/telemetry/spec.md:69`), the same open item recorded at `docs/design/review-2026-10-04-findings.md:29`. Without it the metric emits without the label.

## Risks / Trade-offs

- **Clock skew shortens a TTL** → D3: the store stamps and compares with its own clock; no caller passes an instant.
- **Stored-shape change breaks durable stores** → `MemoryEntry.StoredAt` must be persisted by every `MemoryStore`; the core memory store and any postgres memory store are updated in the same change, and the missing column fails loudly rather than silently dropping the field.
- **Consolidation at `Done` costs one model call and can fail after the run is terminal** → run it after the terminal state is recorded; a consolidation failure is recorded and audited, not promoted to a run failure.
- **`memory_write` denial path may double-count** → the guard denies once per call; the tool does not re-check, so `gohan.memory.write_denied` increments once (D2/D5 split).

## Migration Plan

Additive: no existing caller constructs `notes.Memory` (none exists) and `MemoryEntry.StoredAt` defaults to the zero time for records written before the change. New stores persist the column; an entry with a zero `StoredAt` is treated as already expired and evicted on the next write. Rollback is removing the registration; the store interface and error predate this change.

## Open Questions

None. The one recorded unknown — whether `gohan.memory.evicted` can carry `tenant` — is settled by D9 against the telemetry allow-list.
