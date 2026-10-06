# Design: m1-schema-read-paths

## Context

The M1 matrix assigns `lifecycle.upcast-on-read` and `lifecycle.historical-fixtures` here as integration gaps: the registry and test suite declarations exist, but no reader uses them (`openspec/changes/m1/design.md:160-170`). Registry contract is pure N→N+1 upcasting, additive changes need no upcaster, and stores never rewrite records in place (`openspec/specs/stores/spec.md:334`; `core/stores/schema.go:17-38`). The shared M1 decisions in `openspec/changes/m1/design.md:230-242` do not alter this contract. Root remains pre-v1 through M2.

## Goals / Non-Goals

**Goals:** Run upcasters on every serialized versioned-record read; preserve source bytes; replay historical fixtures through current readers; report incomplete chains and upcast metrics.

**Non-Goals:** No schema-spec amendment, persistence adapter, in-place migration, additive-field upcaster, or release tag.

## Existing public-path inventory and serialized-read scope

`core/stores/schema.go` already provides `SchemaVersion`, `SchemaRegistry`, and `Upcast(shape string, from SchemaVersion, raw []byte) ([]byte, error)`. `Upcast` wraps an absent chain with `types.ErrSchemaTooOld` and a newer version with `types.ErrCheckpointIncompatible`. This package adds `RecordEnvelope` and generic `DecodeRecord[T]` at that package boundary. The envelope carries `Shape string`, `Version SchemaVersion`, and `Payload []byte`; `DecodeRecord` copies `Payload`, calls `Upcast`, then JSON-decodes the result into `T`. Domain types remain free of schema metadata. The envelope is not written back.

The decoder emits `types.TelemetryFrom(ctx).Count(ctx, "gohan.schema.upcast", 1, types.Int("from", int64(envelope.Version)), types.Int("to", int64(registry.current)))` once after each successful non-empty chain and before decoding. Nil telemetry is a no-op. Failed upcasts emit no event. `Upcast` remains the registry's pure transformation boundary; `DecodeRecord` is its persisted-record integration boundary.

No production store in `core/stores` currently has a serialized-record read path. Therefore this change binds **no existing store read method** to `DecodeRecord`. Session history (`MemorySessionLog.Load`), shared state (`MemorySessionLog.SharedStateMeta`), checkpoints (`MemoryCheckpoints.Peek`, `Consume`, `PendingInput`, `ConsumeIf`, `ResumeReady`), audit (`MemoryAuditLog.Read`), events (`MemoryEventLog.Read`), journal (`MemoryJournal.Reserve`, `ByFingerprint`), runs (`MemoryRuns.ByID`, `ByOperation`, listing methods), notes (`MemoryNotes.Read`), and output blobs (`MemoryOutputs.Get`) remain deferred until a serialized implementation or seeded serialized backing path exists. `RedactionMap` has no store type or read path. `OutputStore.Get` returns opaque block bytes and is not a `Message` record decoder.

When serialized implementations exist, every reader that decodes one of the eight shapes in `storedShapes` (`Message`, `State`, `Checkpoint`, `AuditRecord`, `Event`, `Entry`, `Run`, `RedactionMap`) must call `DecodeRecord` immediately before decoding the returned domain value. This list is a binding requirement, not a claim that those paths exist now. No memory store or typed clone is presented as serialized coverage.

## Decisions

### D1 — Envelope, decode, and errors

The ADR fixes the envelope contents, decoder order, copy semantics, error behavior, and no-rewrite rule. Proposed concrete API is `type RecordEnvelope struct { Shape string; Version SchemaVersion; Payload []byte }` and `func DecodeRecord[T any](ctx context.Context, registry *SchemaRegistry, envelope RecordEnvelope) (T, error)`. This signature is the package integration point; serialized readers call it before constructing/returning their domain record. The repository has no such reader call site today, so store-specific bindings are deferred as listed above.

Missing N→N+1 steps return an error wrapping `types.ErrSchemaTooOld`; newer stored versions return an error wrapping `types.ErrCheckpointIncompatible`; upcaster and JSON decode errors propagate with shape/version context. No stale record is returned after failure.

### D2 — Metric

The existing telemetry port is `types.Telemetry.Count(ctx context.Context, name string, n int64, attrs ...types.Attr)` in `core/types/telemetry.go`; `types.TelemetryFrom(ctx)` retrieves it. Emit `gohan.schema.upcast` once per step in a successful chain, with integer `from` and `to` attributes naming that step's source and destination versions. Use `types.Int` for both attributes. Do not emit for a failed chain or a current-version record.

### D3 — Fixture binding

`testkit/storetest.SchemasFactory` and `SchemaRegistry` currently test registry behavior only. Bind fixtures to each implementation's serialized seed path and public read path; the fixture must not inject a typed current-version value. A fixture must snapshot stored envelope bytes before read, assert public read returns the current shape, read raw bytes after the read and compare byte-for-byte, and observe one metric event per successful step with the corresponding integer `{from,to}` pair.

No implementation-specific serialized seed or public reader exists in this tree. `testkit/storetest.Schemas` cannot claim historical fixture coverage until each implementation supplies this binding. Existing memory stores remain excluded.

### D4 — Open questions and admission blockers

- The driver pins the envelope and decoder API: `stores.RecordEnvelope{Shape string, SchemaVersion int, Payload json.RawMessage}` with the JSON codec (every stored payload in this tree is JSON, and upcasters are pure functions over it), and `func DecodeRecord[T any](ctx context.Context, reg *SchemaRegistry, env RecordEnvelope) (T, error)`, which applies the N→N+1 chain to a copy of `Payload`, decodes the result into `T`, and counts `gohan.schema.upcast{from,to}` once per successful step through the `types.Telemetry` from `types.TelemetryFrom(ctx)`. A shape with no registered version, or a chain that stops short of the current version, returns `types.ErrSchemaTooOld` and no value. The envelope is the record as stored: the payload bytes the reader receives are the bytes that were written, so a read never rewrites a record.
- The memory implementations are this change's serialized backing path. Today they return typed values, so no reader consults the registry; this change makes them persist and read `RecordEnvelope` JSON, which is what makes an upcast observable from a public read and what `storetest.Schemas` binds its fixtures to. `adapter/postgres` adopts the same envelope through its own change; this package does not add a serialized adapter for it.
- `testkit/storetest.Schemas` gains the fixture binding: a serialized seed that writes envelope bytes, the implementation's public read, and a byte snapshot taken before and after the read. Registry-only tests prove registry behavior and nothing more.

## Public-path verification

Once an implementation provides a serialized backing path, seed a v1 `Message` envelope through its lowest-level serialized fixture API. Install v1→v2 and v2→v3 pure upcasters in the same registry the public reader uses. Invoke the public read operation reached from `Build`/`Send`/`Resume`; assert v3 fields, exactly one telemetry count for each step (`{from:1,to:2}` and `{from:2,to:3}`), and byte-for-byte equality of the stored envelope before and after. A missing second link must satisfy `errors.Is(err, types.ErrSchemaTooOld)` and return no stale message.

The live tree has no serialized implementation or fixture seed seam, so this public-path proof remains blocked. Registry-only tests in `core/stores/schema_test.go` prove registry behavior only.

## Risks / Trade-offs

- **Risk:** an unused generic decoder could leave persisted readers stale. **Control:** bind each future serialized reader immediately before its domain decode and do not claim store coverage without that path.
- **Risk:** schema metadata could leak into domain types. **Control:** keep version only in `RecordEnvelope`.
- **Risk:** historical fixture bytes may not decode in current readers. **Control:** bind fixtures to actual serialized seeds and public reads.

## Migration Plan

No in-place rewrite or data migration. Existing records retain original bytes and version; readers transform copies. No existing store receives a serialized read path in this change because none exists to bind. Follow-up implementation requires the open API and seed-path decisions above. No release tags during M1 authoring.
