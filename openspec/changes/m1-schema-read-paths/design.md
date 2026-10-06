# Design: m1-schema-read-paths

## Context

The M1 matrix assigns `lifecycle.upcast-on-read` and `lifecycle.historical-fixtures` here as integration gaps: the registry and test suite declarations exist, but no reader uses them (`openspec/changes/m1/design.md:160-170`). Registry contract is pure N→N+1 upcasting, additive changes need no upcaster, and stores never rewrite records in place (`openspec/specs/stores/spec.md:334`; `core/stores/schema.go:17-38`). The shared M1 decisions in `openspec/changes/m1/design.md:230-242` do not alter this contract. Root remains pre-v1 through M2.

## Goals / Non-Goals

**Goals:** Run upcasters on every serialized versioned-record read; preserve source bytes; replay historical fixtures through current readers; report incomplete chains and upcast metrics.

**Non-Goals:** No schema-spec amendment, persistence adapter, in-place migration, additive-field upcaster, or release tag.

## Existing public-path inventory and blocking boundary

The requested inventory was checked against `core/stores/*.go` and its live implementations. This tree has no serialized store decoder in `core/stores`; grep finds no `json.Unmarshal` or other decode call in store production files. The in-memory reference stores retain typed values, so they have no serialized bytes to pass through `SchemaRegistry.Upcast`.

| Shape / path | Current `SchemaVersion` evidence | Read entry point | Registry insertion point |
|---|---|---|---|
| Message (session log) | `types.Message` currently has no store schema field; `History` has no record wrapper | `(*MemorySessionLog).Load` in `core/stores/session_memory.go` | No serialized decode exists. A future serialized adapter must call schema decode before constructing each returned message. |
| State (shared state) | JSON value in `sharedStateRow`; no schema field | `(*MemorySessionLog).SharedStateMeta` | No decode exists; value is returned as `json.RawMessage`. |
| Checkpoint | `stores.Checkpoint.SchemaVersion` exists (`core/stores/checkpoint.go:39-52`) | `Peek`, `Consume`, `PendingInput`, `ConsumeIf`, `ResumeReady` in `core/stores/checkpoint_memory.go` | These clone typed in-memory checkpoints; no serialized decode exists. Persisted adapter decode site is absent. |
| AuditRecord | No `SchemaVersion` field (`core/stores/audit.go:37-64`) | `(*MemoryAuditLog).Read` in `core/stores/audit.go` | Typed memory copy only; no decode exists. |
| Event | No `SchemaVersion` field (`core/stores/events.go:17-20`) | `(*MemoryEventLog).Read` in `core/stores/events.go` | Typed memory copy only; no decode exists. |
| Entry (journal) | No `SchemaVersion` field (`core/stores/journal.go:28-34`) | `(*MemoryJournal).Reserve` and `ByFingerprint` in `core/stores/journal_memory.go` | Typed memory copy only; no decode exists. |
| Run | No `SchemaVersion` field (`core/stores/runs.go:47-66`) | `(*MemoryRuns).ByID`, `ByOperation`, and listing methods in `core/stores/runs.go` | Typed memory copy only; no decode exists. |
| RedactionMap | No corresponding stored shape type or read path found under `core/stores` | None | No integration point exists. |
| OutputStore | Output bytes encode a `types.Message` via `encodeOutput`; blobs contain content, not one of the listed versioned store rows (`core/stores/outputs.go:32-40`) | `(*MemoryOutputs).Get` | `Get` returns opaque block bytes. It must not treat a blob as a `Message` record without a contract. |
| Notes | `NotesStore` stores string plus monotonic CAS version, not schema version (`core/stores/notes.go:30-36`) | `(*MemoryNotes).Read` | Typed in-memory string; no decode exists. |

**Admission blocker:** the normative stores requirement names all eight shapes as versioned records, but live APIs lack schema fields/wrappers for seven shapes and contain no serialized decode paths or persistent store adapters. This package cannot truthfully specify exact insertion call sites for journal, session log, events, outputs, audit, runs, and notes. Adding fields/wrappers to normative shapes is a shared spec amendment, which the driver owns. Do not implement guessed adapters or claim existing memory reads exercise upcasting. The driver must supply the approved record envelope and adapter/read seam before implementation admission.

## Decisions

### D1 — Read transformation and error

The read path applies `SchemaRegistry.Upcast(shape, version, raw)` to a copy of source bytes before decoding the shape. `Upcast` already returns `types.ErrSchemaTooOld` wrapped when any N→N+1 step is absent, and `types.ErrCheckpointIncompatible` for a newer stored version (`core/stores/schema.go:101-124`). Readers return that error; they MUST NOT return stale raw or typed data after an incomplete chain. The stored byte slice is read-only input. Upcasters return a new transformed value and never write to storage.

The repository has no live read decoder to name or change. Do not invent a public `DecodeRecord` signature or wire it to typed memory clones. That exported seam and the mapping from typed records to serialized envelopes remain a blocking design input, not a coder decision.

### D2 — Metric

Emit one counter increment per successful record upcast chain at the registry integration boundary, after the complete chain succeeds and before shape decoding. Instrument name is `gohan.schema.upcast`; attributes are integer `from` and `to` schema versions. No metric API or instrumentation dependency is present in `core/stores` today. The owning driver must select and pin the existing repository telemetry seam before admission; do not add a second telemetry abstraction or emit partial success for a failed chain.

### D3 — Fixture records and version declaration

`testkit/storetest.SchemaFixture` already records `Shape`, `Version`, and JSON `Data` (`testkit/storetest/schemas.go:38-57`). Keep one immutable fixture entry per released schema version for every shape; `Version` is the explicit written-at version. New releases append fixtures and never rewrite old entries. The suite must declare each fixture's version with `SchemaRegistry.Declare(shape, version)`, apply the implementation's registered chain, and load the transformed fixture through that implementation's current reader. `NewSchemaRegistryFor(current)` is the existing test target-version factory contract (`core/stores/schema.go:52-65`).

Current `schemaFixtures` contains one v1 example for each shape. Those bytes are illustrative objects, not proven serializations accepted by all current readers: no common reader factory or persistence implementation exists in `testkit/storetest` (`testkit/storetest/schemas.go:33-36,59-62`). Test fixture replay through current readers is blocked until adapter binding APIs exist. Do not report registry-only replay as satisfying `lifecycle.historical-fixtures`.

### D4 — Public-path verification and immutability

After the driver supplies a real serialized read path, seed its backing store with a `Message` envelope explicitly marked version 1 and JSON that requires both v1→v2 and v2→v3 transformations. Invoke the public store read API, assert resulting v3 fields and exactly one counter event with `{from:1,to:3}`, then fetch the raw stored bytes and compare byte-for-byte with the pre-read snapshot. Also assert missing v2→v3 registration returns an error satisfying `errors.Is(err, types.ErrSchemaTooOld)` and yields no stale shape. Seed through the adapter's lowest-level test fixture insertion path, not by injecting an already-current typed value.

No such adapter seed path exists in this tree. Memory store constructors accept typed values or start empty; therefore the scenario's stored v1 public-path proof is presently blocked, not replaceable by direct `SchemaRegistry.Upcast` tests.

## Risks / Trade-offs

- **Risk:** applying upcasters only in a helper could leave a store read path stale. **Control:** do not admit implementation until each persistent decoder is named and bound to the helper.
- **Risk:** adding schema metadata to the listed record structs can require normative contract edits. **Control:** driver owns any `openspec/specs/` amendment and its ADR; this authoring package makes none.
- **Risk:** fixture decoding cannot be inferred from sample JSON. **Control:** each fixture must be accepted by the real reader binding for its shape and released version.

## Migration Plan

No in-place rewrite or data migration. Existing records retain original bytes and version; readers transform copies. Any record-shape amendment, ADR, and types-index regeneration follow repository change workflow and require driver ownership. No release tags during M1 authoring.