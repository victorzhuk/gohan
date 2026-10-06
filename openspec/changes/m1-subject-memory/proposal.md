# Proposal

## Why

Subject memory ships only as a store interface and an error: `stores.MemoryStore` is declared with its three methods at `core/stores/optional.go:23`, `ErrMemoryStoreRequired` ships at `core/types/errors.go:63`, and the reference store implements `Put`/`Entries`/`Forget` at `core/stores/optional.go:50`. Nothing else is wired — no preset registers `memory_write` or `memory_read` (`std/presets.go` carries only prompt text), `MemoryEntry` carries no timestamp (`core/stores/optional.go:11`), and the store enforces no TTL. M1 owes seven `working-state` scenarios on the public path (`openspec/changes/m1/design.md:107-113`): six are recorded `integration gap` and `working-state.memory-ttl-eviction` is recorded `missing`.

## What Changes

- **Added** `MemoryPolicy{Schema json.RawMessage, MaxEntries int, TTL time.Duration}` and `notes.Memory(store, policy, opts...)`, which registers the `memory_write`/`memory_read` tool pair (`core/tool_names.go:22-23` already reserves both names). `notes.AllowUntrustedMemory()` is the opt-out option.
- **Added** a store-clock timestamp on `MemoryEntry` and store-side eviction: the store stamps each entry, and evicts the oldest expired entry past `MaxEntries`/`TTL` at write time, counting `gohan.memory.evicted`.
- **Added** store-boundary schema validation of the written value against `MemoryPolicy.Schema`.
- **Added** denial of `memory_write` on an untrusted turn at the `StageContext` guard (`openspec/specs/guards/spec.md:67`), counted `gohan.memory.write_denied`.
- **Added** origin carriage: `memory_read` results carry each entry's stored `Origins` so taint rule 6 applies (`openspec/specs/taint/spec.md:52`).
- **Added** consolidation: at `Done`, one `std/flow.Extract` (`std/flow/extract.go:45`) over the session produces schema-valid entries, audited `memory_consolidate` with the source session.
- **Added** `Stack.Memory(ctx, owner)` and `Stack.ForgetMemory(ctx, owner, ids...)`, owner-checked like `Inspect` (`core/inspect.go:35`).
- **Note** `MemoryEntry`'s stored shape and `MemoryPolicy` gain fields, so the `working-state` subject-memory Contract structs change. Subject erasure already reads the `MemoryEraser` seam (`core/stores/erasure.go:55`, wired at `core/erasure.go:72`) and is unchanged.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `working-state`: the *Subject memory* requirement gains the normative store contract (schema validation, store-clock timestamp, TTL/`MaxEntries` eviction) and the public tool/control surfaces it names. Its seven deferred scenarios gain their implementation; the scenarios themselves are unchanged.

## Impact

- Code: `core/stores/optional.go` (`MemoryEntry`, eviction, validation), `std/notes` (new `Memory` constructor and pair), `core` (`Build` wiring, `Stack.Memory`, `Stack.ForgetMemory`), `core/tool_names.go` (names already present).
- Contract: `MemoryPolicy` and `MemoryEntry.StoredAt` become normative.
- Metrics: `gohan.memory.evicted` — its `tenant` label requires `WithTenantLabel()` (`openspec/specs/telemetry/spec.md:69`).
- Specs: `working-state` only. No change to `taint`, `guards`, `redaction` or `stores` text.
