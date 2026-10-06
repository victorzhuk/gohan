# Design: m1-schema-read-paths

## Context

The M1 matrix assigns `lifecycle.upcast-on-read` and `lifecycle.historical-fixtures` here as integration gaps: the registry and test suite declarations exist, but no reader uses them (`openspec/changes/m1/design.md:160-170`). Registry contract is pure N→N+1 upcasting, additive changes need no upcaster, and stores never rewrite records in place (`openspec/specs/stores/spec.md:334`; `core/stores/schema.go:17-38`). The shared M1 decisions in `openspec/changes/m1/design.md:230-242` do not alter this contract. Root remains pre-v1 through M2.

## Goals / Non-Goals

**Goals:** Run upcasters on every serialized versioned-record read; preserve source bytes; replay historical fixtures through current readers; report incomplete chains and upcast metrics.

**Non-Goals:** No schema-spec amendment, persistence adapter, in-place migration, additive-field upcaster, or release tag.

## Existing public-path inventory and serialized-read scope

`core/stores/schema.go` already provides `SchemaVersion`, `SchemaRegistry`, and `Upcast(shape string, from SchemaVersion, raw []byte) ([]byte, error)`. `Upcast` wraps an absent chain with `types.ErrSchemaTooOld` and a newer version with `types.ErrCheckpointIncompatible`. This package adds `stores.RecordEnvelope{Shape string, SchemaVersion int, Payload json.RawMessage}` and generic `DecodeRecord[T]` at that package boundary. The envelope is JSON; `DecodeRecord` copies `Payload`, applies `Upcast`, then JSON-decodes the result into `T`. Domain types remain free of schema metadata. The envelope is not written back.

The decoder emits `types.TelemetryFrom(ctx).Count(ctx, "gohan.schema.upcast", 1, types.Int("from", int64(envelope.SchemaVersion)), types.Int("to", int64(registry.current)))` once after each successful non-empty chain and before decoding. Nil telemetry is a no-op. Failed upcasts emit no event. `Upcast` remains the registry's pure transformation boundary; `DecodeRecord` is its persisted-record integration boundary.

Memory implementations are this change's serialized backing path. The existing typed memory stores must persist and read `RecordEnvelope` JSON so public reads consult the registry. Bind `MemorySessionLog.Load`, `MemorySessionLog.SharedStateMeta`, checkpoint reads, `MemoryAuditLog.Read`, `MemoryEventLog.Read`, journal reads, run reads, `MemoryNotes.Read`, and `MemoryOutputs.Get` only where their returned shapes are in `storedShapes`. `RedactionMap` has no store type or read path. `OutputStore.Get` returns opaque blocks and is not a schema-record reader.

Every reader that decodes one of the eight shapes in `storedShapes` (`Message`, `State`, `Checkpoint`, `AuditRecord`, `Event`, `Entry`, `Run`, `RedactionMap`) must call `DecodeRecord` immediately before decoding the returned domain value. Adapter bindings remain deferred until their owning change provides a serialized adapter. No typed clone is presented as serialized coverage.

## Decisions

### D1 — Envelope, decode, and errors

The ADR fixes the envelope contents, decoder order, copy semantics, error behavior, and no-rewrite rule. The concrete API is `stores.RecordEnvelope{Shape string, SchemaVersion int, Payload json.RawMessage}` and `func DecodeRecord[T any](ctx context.Context, reg *SchemaRegistry, env RecordEnvelope) (T, error)`. Use the JSON codec. This signature is the package integration point; serialized readers call it before constructing or returning their domain record. Memory implementations persist and read these envelopes as this change's serialized backing path.

Missing N→N+1 steps return an error wrapping `types.ErrSchemaTooOld`; newer stored versions return an error wrapping `types.ErrCheckpointIncompatible`; upcaster and JSON decode errors propagate with shape/version context. No stale record is returned after failure.

### D2 — Metric

The existing telemetry port is `types.Telemetry.Count(ctx context.Context, name string, n int64, attrs ...types.Attr)` in `core/types/telemetry.go`; `types.TelemetryFrom(ctx)` retrieves it. Emit one `gohan.schema.upcast` counter event for each successful chain, with integer `from` and `to` attributes naming the chain's original and final versions. For a v1 record read by v3 code, emit one event with `{from:1,to:3}`. Use `types.Int` for both attributes. Do not emit for a failed chain or a current-version record.

### D3 — Fixture binding

`testkit/storetest.SchemasFactory` and `SchemaRegistry` currently test registry behavior only. Bind fixtures to the memory implementations' serialized seed path and public read path; the fixture must not inject a typed current-version value. A fixture must snapshot stored envelope bytes before read, assert public read returns the current shape, read raw bytes after the read and compare byte-for-byte, and observe one metric event per successful chain with the chain's integer `{from,to}` pair.

The memory implementations provide this change's serialized seed and public reader binding. Registry-only tests cannot claim historical fixture coverage. Adapter bindings remain deferred until an adapter provides a serialized path.

### D4 — Open questions and admission blockers

The driver pins the envelope and decoder API: `stores.RecordEnvelope{Shape string, SchemaVersion int, Payload json.RawMessage}` with the JSON codec (every stored payload in this tree is JSON, and upcasters are pure functions over it), and `func DecodeRecord[T any](ctx context.Context, reg *SchemaRegistry, env RecordEnvelope) (T, error)`, which applies the N→N+1 chain to a copy of `Payload`, decodes the result into `T`, and counts `gohan.schema.upcast{from,to}` once per successful chain through the `types.Telemetry` from `types.TelemetryFrom(ctx)`. A shape with no registered version, or a chain that stops short of the current version, returns `types.ErrSchemaTooOld` and no value. The envelope is the record as stored: the payload bytes the reader receives are the bytes compared before and after read.
- The memory implementations are this change's serialized backing path. They currently return typed values, so no reader consults the registry; this change makes them persist and read `RecordEnvelope` JSON, which makes an upcast observable from a public read and lets `storetest.Schemas` bind its fixtures. `adapter/postgres` adopts the same envelope through its own change; this package does not add a serialized adapter for it.
- `testkit/storetest.Schemas` gains the fixture binding: a serialized seed that writes envelope bytes, the implementation's public read, and a byte snapshot taken before and after the read. Registry-only tests prove registry behavior and nothing more.

## Public-path verification

Once a memory implementation provides its serialized backing path, seed a v1 `Message` envelope through its lowest-level serialized fixture API. Install v1→v2 and v2→v3 pure upcasters in the same registry the public reader uses. Invoke the public read operation reached from `Build`/`Send`/`Resume`; assert v3 fields, exactly one telemetry count with `{from:1,to:3}`, and byte-for-byte equality of the stored envelope before and after. A missing second link must satisfy `errors.Is(err, types.ErrSchemaTooOld)` and return no stale message.

The live tree has no serialized implementation or fixture seed seam, so this public-path proof remains blocked. Registry-only tests in `core/stores/schema_test.go` prove registry behavior only.

## Risks / Trade-offs

- **Risk:** an unused generic decoder could leave persisted readers stale. **Control:** bind each future serialized reader immediately before its domain decode and do not claim store coverage without that path.
- **Risk:** schema metadata could leak into domain types. **Control:** keep version only in `RecordEnvelope`.
- **Risk:** historical fixture bytes may not decode in current readers. **Control:** bind fixtures to actual serialized seeds and public reads.

## Migration Plan

No in-place rewrite or data migration. Existing records retain original bytes and version; readers transform copies. No existing store receives a serialized read path in this change because none exists to bind. Follow-up implementation requires the open API and seed-path decisions above. No release tags during M1 authoring.
