# Proposal: m1-anthropic-provider

## Why

The `model` spec pins the provider adapter contract (`openspec/specs/model/spec.md:292`) and the shared conformance suite exposes eight registered adapter scenarios (`testkit/conformance/model.go:49,54-63`), but no Anthropic adapter exists: `adapter/` holds only `otel` (`openspec/changes/m1/design.md:212`). The milestone assigns this adapter the eight `model.*` scenario IDs plus error normalization and version reporting. Until it lands and runs the suite, those eight IDs stay unearned and no run can reach an Anthropic endpoint.

## What Changes

- Add the Go module `adapter/anthropic` (`github.com/victorzhuk/gohan/adapter/anthropic`), a separate module mirroring `adapter/otel`'s layout: a `types.Model` implementation in package `anthropic` plus its own conformance test package.
- Translate between `types.ModelRequest`/`types.ModelChunk` and the Anthropic Messages API over a streaming HTTP call: error-class normalization via `types.ClassifyProviderError`, `Retry-After` parsing, `FinishReason` mapping, opaque `Reasoning` signature round-trip, `Raw` block pass-through.
- Enforce the adapter's call-time duties: a dynamic governed-key collision in `ModelOptions.Extra` fails with `ClassPermanent` before the request is sent; `ResponseSchema` is honoured through a forced tool only when `Caps.Constrained` is declared; `CacheBreak` blocks map to ordered `cache_control` markers capped by `Caps.CacheBreakpoints`.
- Ship recorded Anthropic wire fixtures and a fixture-backed `types.Model`; the adapter's `TestModelConformance` runs `conformance.ModelScenarios` over them and reports the eight registered IDs.
- Prove public Send behaviour separately: `Extra` collision, schema refusal, cache markers, `Retry-After` propagation, `Raw` fidelity and key secrecy.
- Wire the module into `go.work` and the module lists in `Taskfile.yml` (`:17`, `:20`, `:30-32`, `:70`, `:82`).

No breaking changes.

## Capabilities

### New Capabilities

- None. The adapter implements the existing `model` capability contract rather than introducing a new behavioural capability.

### Modified Capabilities

- `model`: one added requirement states the adapter-specific Anthropic wire mapping (ordered `cache_control` markers, `stop_reason` to `FinishReason`, constrained output through a forced tool, opaque reasoning signature round-trip). Its scenarios carry no `ID:` lines because the shared suite already claims the eight registered `model.*` ids.

## Impact

- New module `adapter/anthropic` (`go.mod`, package `anthropic`, conformance and Send tests), added to `go.work` and `Taskfile.yml`.
- Depends on `m1-provider-conformance-contract` for `conformance.ModelScenarios`, `FixtureKey` and the fixture kinds (`testkit/conformance/model.go:49`, `testkit/conformance/fixtures.go:58`), and on `m1-provider-metadata-contract` for the request-metadata field.
- No `openspec/specs/` behaviour other than the one added `model` requirement; no change to `core`, `std`, or the root module.
- No release tag: `api:check` skips an untagged module (`Taskfile.yml:53`).
