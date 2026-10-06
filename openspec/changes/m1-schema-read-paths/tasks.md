# Tasks

## Status

Delivered and green: `stores.RecordEnvelope` and the generic `stores.DecodeRecord[T]` at the
schema boundary, counting one `gohan.schema.upcast{from,to}` event per successful chain with
the chain's endpoints through the telemetry in the context, returning `types.ErrSchemaTooOld`
and no value when a version or a link is missing, and leaving the stored bytes untouched. The
memory `SessionLog` and `Checkpoints` now persist and read that envelope, and the checkpoint
read keeps the version a resumed run's writes compare against.

Remaining: the fixture binding, and it is blocked on a named seam. `testkit/storetest` can
only reach an implementation through the interface it is handed, and the memory stores expose
no serialized seed or raw read — a fixture therefore cannot write envelope bytes through the
implementation's own path, which is what `lifecycle.historical-fixtures` needs. Seven shapes
have no serialized read path at all: `State`, `AuditRecord`, `Event`, `Entry`, `Run`,
`RedactionMap`, and the checkpoint fixture binding.

## 1. Authoring

- [x] 1.0 Apply ADR-0157 decision 7: add `stores.RecordEnvelope` and generic `DecodeRecord[T]` at the schema boundary, use `types.TelemetryFrom(ctx)` for the metric, and bind fixtures only to serialized seeds and public readers. Evidence: ADR-0157 decision 7; `core/stores/schema.go`; `core/types/telemetry.go`; `core/stores/schema_record_test.go`. Verify: `go test -short -timeout 2m ./core/stores -run 'TestDecode'`.
- [x] 1.1 Record ADR-0157's envelope and telemetry decisions, the concrete proposed API, all existing typed read paths that remain unbound, and the open adapter/fixture binding questions in `design.md`. Verify: `openspec validate m1-schema-read-paths --strict --no-interactive`.
- [x] 1.2 Bind only `lifecycle.upcast-on-read` and `lifecycle.historical-fixtures` in the lifecycle delta. Verify: `openspec validate m1-schema-read-paths --strict --no-interactive`.

## 2. Implementation

- [x] 2.1 Versioned record envelopes and their read API for the shapes reached so far: `Message` and `Checkpoint` persist and decode through `RecordEnvelope`; the checkpoint fix keeps `ConsumeIf` and `UpdatePending` comparing the same decoded value `Peek` returns, so a resumed run's append is not rejected as stale. Verify: `go test -short -timeout 2m ./core/...`.
- [x] 2.2 Registry upcasting before decode on those read paths, `types.ErrSchemaTooOld` on an incomplete chain, no stale value returned, source bytes preserved, one counter event per successful chain with integer `from` and `to` attributes. Verify: `go test -short -timeout 2m ./core/stores -run 'TestCheckpoint'`.
- [ ] 2.3 Bind `storetest.Schemas` to serialized memory-store seeds and public readers with immutable fixtures. Blocked: the memory stores expose no serialized seed or raw read for the suite to use, so the fixture would have to invent an adapter contract or test a synthetic store. Resolve by giving the suite one seam the implementations share, then bind.
  - verify: `go test -short -timeout 2m ./testkit/storetest -run 'TestSchemasSerializedPublicRead'`
- [ ] 2.4 Seed a serialized v1 `Message`, read it through the store's own read path with v1→v2→v3 upcasters, assert the value received and the metric fired once with `{from:1,to:3}`. The `Message` serialized path exists and its decode-level behaviour is covered; the fixture-level proof waits on 2.3.
  - verify: `go test -short -timeout 2m ./core/stores -run 'TestMessageUpcast'`
- [ ] 2.5 Run package and repository gates after implementation and all owning lanes land. Verify: `task lint` and `task test`.

## 3. Remaining shapes

- [ ] 3.1 A serialized read path for `State`, `AuditRecord`, `Event`, `Entry`, `Run` and `RedactionMap`. Each needs the envelope at its write and read boundary; `RedactionMap` has no stored shape yet (it arrives with `m1-redaction-pipeline`). Verify: `go test -short -timeout 2m ./core/stores/...`.
- [ ] 3.2 Validate this change and the M1 scenario acceptance once every M1 owner has landed. Verify: `openspec validate m1-schema-read-paths --strict --no-interactive` and `task spec:gate GATE=M1`.
