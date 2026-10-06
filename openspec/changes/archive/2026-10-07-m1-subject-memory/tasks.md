# Tasks

## 1. Store contract

- [x] 1.1 Add `StoredAt time.Time` to `MemoryEntry` and `MemoryPolicy{Schema, MaxEntries, TTL}` to `core/stores`, defaulting `MaxEntries` to 64 and `TTL` to 90 days; verify `go build ./...` and that `core/stores/optional.go` still compiles with the widened struct.
  - evidence: `core/stores/optional.go` (`MemoryEntry.StoredAt`, `MemoryPolicy`, `DefaultMemoryMaxEntries(64)`, `DefaultMemoryTTL(90d)`, `NewMemoryNotes`, `WithMemoryNotesClock`, `WithMemoryNotesPolicy`); `go build ./...` green.
- [x] 1.2 Make `MemoryNotes.Put` reject a value that does not validate against the policy `Schema` and return `ErrMemoryWriteInvalid` (new `types` error class); verify a unit test asserts a free-text value is refused and nothing is stored.
  - evidence: `core/stores/memory_schema.go` (strict-schema validator over type/enum/required/properties/additionalProperties/items/min-max), `core/types/errors.go` (`ErrMemoryWriteInvalid`), `core/stores/memory_test.go` (refusal leaves the store empty).
- [x] 1.3 Make `MemoryNotes.Put` stamp `StoredAt` from the store clock and evict expired entries plus the oldest past `MaxEntries` before inserting, keeping survivors' versions; verify a unit test writes `MaxEntries+1` entries and asserts the oldest is gone (`working-state.memory-ttl-eviction`).
  - evidence: `core/stores/memory_test.go` (`working-state.memory-ttl-eviction` drives the store clock; expired and oldest entries evicted, survivors keep versions).
- [x] 1.4 Emit `gohan.memory.evicted` on each eviction, registering it with the telemetry policy so `tenant` is present only under `WithTenantLabel()`; verify the metric-registration test fails an unregistered label and passes with it.
  - evidence: emission in `core/stores/optional.go`, registration `std/telemetry/metrics.go` (`MetricMemoryEvicted`), `std/telemetry/metrics_test.go` (`memoryEvictedRegistration`) proves the label policy.

## 2. Memory tools and wiring

- [x] 2.1 Add `notes.Memory(store stores.NotesStore, policy stores.MemoryPolicy, opts ...Option) (types.Tool, types.Tool, error)` registering `memory_write` and `memory_read` (`ReadOnly`; `memory_write` `Trusted`, `Risk: High`), plus `notes.AllowUntrustedMemory()`; verify a unit test constructs both tools and that `memory_write` on a store without `MemoryStore` fails `ErrMemoryStoreRequired`.
  - evidence: `std/notes/memory.go` (`Memory`, `AllowUntrustedMemory`), `std/notes/memory_test.go` (registration, `ErrMemoryStoreRequired`). `memory_write` is `types.SideEffect`, `Risk: High`, trusted; `memory_read` is `ReadOnly`.
- [x] 2.2 Verify the already-shipped optional-interface fallback for `working-state.memory-store-optional` still satisfies the scenario: the fallback is recorded as shipped in `openspec/changes/archive/2026-10-05-m0-core/tasks.md:927,966` and the scenario carries no `deferred_to` in `openspec/scenarios.json`; wire the pair through `Build` from the flow's resolved `memory` policy so a registered memory tool on a store lacking `MemoryStore` fails `Build` with `ErrMemoryStoreRequired`; verify `core/egress_build`-style build test asserts both the failure and that session notes keep working.
  - evidence: the shipped fallback is re-verified in `std/notes/memory_test.go` (`working-state.memory-store-optional`); `core/native_resolve.go` fails `Build` with `ErrMemoryStoreRequired` when a declared `MemoryPolicy` meets a store without `MemoryStore`, and `core/consolidation_test.go` proves a no-policy flow is unchanged.
- [x] 2.3 Build the write key as `NotesKey{ScopeSubject, tenant, subject}` from `types.RunInfoFrom(ctx)` only, never from tool arguments; verify a test that a run for subject `u1` cannot read `u2`'s entry and cannot construct `u2`'s key (`working-state.subject-memory-isolated`).
  - evidence: `std/notes/memory.go` (`memoryKey` from `types.RunInfoFrom(ctx)`), `std/notes/memory_test.go` (`working-state.subject-memory-isolated`: `u1` cannot see or address `u2`'s entries; forged tenant/subject arguments land on `u1`).

## 3. Guard, origin and consolidation

- [x] 3.1 Route `memory_write` input through the `StageContext` guard, denying with `Failed(Permanent)` and `gohan.memory.write_denied` when the taint window holds an `Untrusted` block and the flow declared no `AllowUntrustedMemory()`; verify a test placing a web-page tool result in the window asserts the deny and the counter (`working-state.memory-write-untrusted-denied`).
  - evidence: `std/notes/memory.go` (guard route + denial), `std/telemetry/metrics.go` (`MetricMemoryWriteDeny`), `std/notes/memory_test.go` (`working-state.memory-write-untrusted-denied`, allow-untrusted opt-out).
- [x] 3.2 Attach each entry's stored `Origins` to the `memory_read` result blocks as `OriginTool{"memory_read"}`; verify a test passes a recalled value to an `Exfil` tool and asserts the default policy denies (`working-state.memory-read-carries-origin`).
  - evidence: `std/notes/memory.go` (one `Text` block per entry carrying `OriginTool{"memory_read"}` over the stored origins), `std/notes/memory_test.go` (`working-state.memory-read-carries-origin`).
- [x] 3.3 Record `Origins` and `RunID` on every write from the writing turn's blocks; verify a unit test asserts the stored entry carries the source block's origin and the run id.
  - evidence: `std/notes/memory.go` stamps the distinct taint-window block origins and `RunID`; `std/notes/memory_test.go` (`memoryOriginRecorded`).
- [x] 3.4 Run one `std/flow.Extract` over `SessionLog` at `Done` when the flow declares a `memory` policy, writing schema-valid entries and auditing `memory_consolidate` with the source session; assert compaction writes none (`working-state.memory-consolidate-at-done`).
  - evidence: `std/notes/consolidate.go` (`Consolidation.Run`: one `flow.Extract` with `flow.WithSchema(policy.Schema)`, entry `consolidated-<RunID>`, `memory_consolidate` audit record naming the source session, compaction-only history a no-op) with `std/notes/consolidate_test.go`; wired at the Done boundary in `core/drive_lifecycle.go` through `core/consolidation.go`, covered by `core/consolidation_test.go` (public `Send` → `Done`).

## 4. Controls and verification

- [x] 4.1 Add `Stack.Memory(ctx, owner)` and `Stack.ForgetMemory(ctx, owner, ids...)` with the `Inspect` owner check (`core/inspect.go:35`), refusing a non-owner with `ErrSessionForbidden`; verify a test that `ForgetMemory(owner, id)` drops the entry from both `Stack.Memory` and the next `memory_read` (`working-state.memory-forget`) and a non-owner call is refused.
  - evidence: `core/memory.go` mirrors `Inspect`'s ownership check (tenant match, subject match or `ScopeSessionRead`), `core/memory_test.go` (`working-state.memory-forget`, non-owner refused with `ErrSessionForbidden`). Note: `core/inspect.go:35` holds `errInspectStoresRequired`, not the owner check; the check lives in `Inspect`'s body and was mirrored rather than invented.
- [x] 4.2 Run `openspec validate m1-subject-memory --strict --no-interactive` and `task spec:gate GATE=M1`; verify both pass with the seven IDs bound.
  - evidence: `Change 'm1-subject-memory' is valid`; `task spec:gate GATE=M1` `unregistered=0`, no missing `working-state.*` id owned here.
- [x] 4.3 Run `task test` and `task lint`; verify green.
  - evidence: `task lint` 0 issues; `task test` green; `task spec` `missing_in_scope=0`; `task api:check` reports the `otel` module compatible.
- [x] 4.4 Update the capability doc if the stored shape surfaces there and verify `task spec` reports no unregistered scenario for the seven IDs.
  - evidence: the stored shape lives in `core/stores` and does not surface in the `working-state` Contract block, so no doc change is needed; `task spec` reports `unregistered=0`.

## Workflow follow-up

- Archive the change after the project's review requirements are satisfied.
