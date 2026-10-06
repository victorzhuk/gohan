# Tasks

- [ ] 1.0 Apply ADR-0157 decision 7: add `stores.RecordEnvelope` and generic `DecodeRecord[T]` at the schema boundary, use `types.TelemetryFrom(ctx)` for per-step metrics, and bind fixtures only to serialized seeds and public readers. Record adapter bindings as deferred where no serialized path exists.
  - Evidence: `docs/adr/0157-capability-ports-record-envelopes-and-journal-arguments.md:81-88`; `core/stores/schema.go`; `core/types/telemetry.go`; design § Existing public-path inventory and serialized-read scope.
  - verify: `openspec validate m1-schema-read-paths --strict --no-interactive`
## 1. Authoring

- [x] 1.1 Record ADR-0157's envelope and telemetry decisions, the concrete proposed API, all existing typed read paths that remain unbound, and the open adapter/fixture binding questions in `design.md`.
  - Evidence: ADR-0157 decision 7; `core/stores/schema.go`; `core/types/telemetry.go`; design § D1–D4.
  - verify: `openspec validate m1-schema-read-paths --strict --no-interactive`
- [x] 1.2 Bind only `lifecycle.upcast-on-read` and `lifecycle.historical-fixtures` in the lifecycle delta; require upcast error behavior, byte preservation, metric attributes, and fixture reader replay.
  - Evidence: assigned rows `openspec/changes/m1/design.md:169-170`; normative requirement `openspec/specs/stores/spec.md:334`; lifecycle scenarios `openspec/specs/lifecycle/spec.md:38-46`.
  - verify: `openspec validate m1-schema-read-paths --strict --no-interactive`

## 2. Implementation (blocked pending shared contract and live adapter seams)

- [ ] 2.1 Obtain driver-approved versioned record envelopes and read API integration points for all normative shapes; add schema version metadata to shapes that lack it only through driver-owned normative spec amendment and ADR. Verify `Message`, `State`, `Checkpoint`, `AuditRecord`, `Event`, `Entry`, `Run`, and `RedactionMap` each declare stored version.
  - verify: `go test -timeout 2m ./core/stores -run 'Test.*SchemaVersion'`
- [ ] 2.2 Wire registry upcasting before decode on each serialized memory-store read path; return `types.ErrSchemaTooOld` on an incomplete chain, never return stale data, and preserve source bytes. Emit one `gohan.schema.upcast` counter event per successful chain with integer `from` and `to` attributes.
  - verify: `go test -timeout 2m ./core/stores -run 'Test.*UpcastRead'`
- [ ] 2.3 Bind `storetest.Schemas` to serialized memory-store seeds and public readers with immutable fixtures for each stored shape at every released version. Fixture `SchemaVersion` declares the original schema. Assert fixture replay reaches current readers without error.
  - verify: `go test -timeout 2m ./testkit/storetest -run 'TestSchemasSerializedPublicRead'`
- [ ] 2.4 Seed a serialized v1 `Message`, read it through the public store path using v1→v2→v3 upcasters, assert output and metric, and compare persisted bytes before and after. Assert a missing link wraps `types.ErrSchemaTooOld` and returns no stale message.
  - verify: `go test -timeout 2m ./core/stores -run 'TestMessageUpcastPublicRead'`
- [ ] 2.5 Run package and repository gates after implementation and all owning lanes land.
  - verify: `task lint` and `task test`
- [ ] 2.6 Validate this change and full M1 scenario acceptance only when M1 implementation owners land.
  - verify: `openspec validate m1-schema-read-paths --strict --no-interactive` and `task spec:gate GATE=M1`
