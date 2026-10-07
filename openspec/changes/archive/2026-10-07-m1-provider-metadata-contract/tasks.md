# Tasks

## 1. Authoring

- [x] 1.1 Add the `model` delta: the complete "Provider adapter contract" requirement, its nine registered scenarios verbatim, and the new normative request-metadata text with two unregistered scenarios. Evidence: `specs/model/spec.md`.
- [x] 1.2 Add the `lifecycle` delta: the complete "Retirement, schema evolution, cost" requirement, its seven registered scenarios verbatim, and the cost-tags paragraph naming `ModelRequest.Metadata`. No scenario claimed. Evidence: `specs/lifecycle/spec.md`.
- [x] 1.3 Name the field, its shape, its population site, the map-or-omit adapter duty and the never-content rule. Evidence: `design.md` D1–D5; adapters marked implementers in D7 and the `model` requirement.
- [x] 1.4 Validate this package. Evidence: `openspec validate m1-provider-metadata-contract --strict --no-interactive` → valid.

## 2. Request metadata field and assembly

- [x] 2.1 Add `Metadata RequestMetadata` to `ModelRequest` and define `type RequestMetadata struct { CostTags CostTags }` in `core/types/model_request.go` (`core/types/model_request.go:11`). Verify: `go build ./...` succeeds and a unit test round-trips `CostTags{Feature, Environment, CostCenter}` through the field.
  - evidence: `core/types/model_request.go` defines `Metadata RequestMetadata` / `RequestMetadata{CostTags}`; `core/types/model_request_test.go` `TestModelRequest` round-trips the three tags.
- [x] 2.2 Populate `Metadata.CostTags` from `in.Run.CostTags` (`core/types/identity.go:80`) in `StablePrefix.Assemble` (`std/assembly.go`, beside the `feature`/`cluster`/`costCtr` reads at `:92-94`). Verify: `go test -short -timeout 2m ./std/... -run TestAssembleMetadata` asserts the three tags on the assembled request.
  - evidence: `std/assembly.go:355` populates `Metadata.CostTags` from `in.Run.CostTags`; `std/assembly_test.go` `TestAssembleCarriesCostTags` asserts the three tags (test named `TestAssembleCarriesCostTags`, not `TestAssembleMetadata`).
- [x] 2.3 Assert the values are never content: a run assembled and logged at `Debug` with content capture on produces no log record and no span content capture carrying the tags. Verify: `go test -short -timeout 2m ./core/... -run TestMetadataNeverContent` asserts absence in both sinks.
  - evidence: `core/metadata_test.go` `TestMetadataNeverContent` captures Debug slog records and span recorder output, asserts the sentinel tags absent from both, with non-vacuity guards.

## 3. Spec application

- [x] 3.1 When applied: edit the main `model` Contract `ModelRequest` block to add the field, write the ADR for the new normative field, and regenerate `docs/design/types.md` (`openspec/project.md:27`). Verify: `git diff openspec/specs/model/spec.md docs/design/types.md` shows the field; `openspec validate m1-provider-metadata-contract --strict --no-interactive` still passes.
  - evidence: `docs/adr/0159-request-metadata-cost-tags-never-content.md` records the field; `task spec:types` regenerated `docs/design/types.md`, which carries the `RequestMetadata` row.
- [x] 3.2 Confirm no `CHANGELOG.md` Breaking entry is needed (the field is zero-valued and additive) and that the `api:check` pre-v1 difference list records the added field. Verify: `task api:check` exit 0 with the field in the reported differences.
  - evidence: `task api:check` exit 0 with `RequestMetadata` named in the difference list; no Breaking entry needed (additive, zero-valued).

## 4. Adapter lanes (implementers)

- [x] 4.1 `adapter/openai`: map `Metadata.CostTags` onto the provider's documented request-metadata channel and name the concrete Send function that does so. Blocked: `adapter/openai` does not exist (`adapter/` holds `otel` only). Verify: the adapter's Send test asserts the outgoing payload carries the three tags; the provider's no-channel path is covered by 4.3.
  - evidence: `adapter/openai/request.go` `assembleBody` maps the three tags to the outgoing `metadata` map, omitting it on empty tags; `adapter/openai/send_test.go` `TestMetadataCostTags` ("carries the three tags on the outgoing body", "zero cost tags send no metadata"). Stale note removed: `adapter/openai` exists.
- [x] 4.2 `adapter/anthropic`: map `Metadata.CostTags` onto the provider's documented request-metadata channel and name the concrete Send function. Blocked: `adapter/anthropic` does not exist. Verify: as 4.1 against recorded responses.
  - evidence: `adapter/anthropic/request.go` never writes cost tags onto the wire (Anthropic has no request-metadata channel); `adapter/anthropic/send_test.go` `cost-tags-never-sent` asserts the unsent tags succeed. Stale note removed: `adapter/anthropic` exists.
- [x] 4.3 One adapter whose provider has no metadata channel leaves the tags unsent and does not fail. Verify: a recorded-response test asserts the request succeeds with no metadata field in the outgoing payload and no error.
  - evidence: `adapter/anthropic/send_test.go` `cost-tags-never-sent` — recorded response, no metadata field, no error.

## 5. Repository gates

- [x] 5.1 Run the repository gates after implementation lands. Verify: `task lint && task test && task spec && task api:check`; `task spec` reports `missing_in_scope=0`.
  - evidence: driver ran `task lint` (0 issues) and `task test` green across root, `adapter/otel`, `adapter/openai`, `adapter/anthropic`, `examples`; `task spec` and `task api:check` green as in 3.2.
