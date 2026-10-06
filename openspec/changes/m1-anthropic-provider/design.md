# Design: m1-anthropic-provider

## Context

The obligation is the eight adapter-actor `model.*` IDs (`m1/design.md:98-103`) run against recorded Anthropic responses, plus error normalization and version reporting (`m1/design.md:212`). The suite that owns the IDs is `testkit/conformance`: `ModelScenarios` (`testkit/conformance/model.go:49`) maps each ID to fixture kinds (`:54-63`) and dispatches through `runFixture` (`:140`). Its assertions are `assertErrorFixture` (`:218`), `assertCacheFixture` (`:164`), `assertSchemaFixture` (`:195`), `assertUsageFixture` (`:241`), `assertTruncationFixture` (`:298`) and `assertRawFixture` (`:338`); the probe request is built by `probeRequest` (`:383`).

The port is `types.Model` (`core/types/model.go:11`): `Profile() ModelProfile` and `Generate(ctx, req) iter.Seq2[ModelChunk, error]`. Chunk and usage types are `core/types/model_types.go:24` and `:33`. The request wire codec is `ModelRequest.MarshalJSONTo` (`core/types/model_request.go:116`). The error surface is `types.ModelError` (`core/types/errors.go:180`) with `Retryable()` (`:198`) and the single classifier `types.ClassifyProviderError(status, code)` (`core/types/model_profile.go:132`). `Caps` (`core/types/model_profile.go:49`) carries `Constrained` (`:52`), `CacheBreakpoints` (`:58`) and `CacheExplicit` (`:103`). `CacheBreak` and `Raw` are `core/types/message.go:122-123`; `ToolUse` is `:108`. The governed option key list is `types.GovernedModelOptionKeys()` (`core/types/model_request.go:29`).

The module layout to mirror is `adapter/otel`: `adapter/otel/go.mod` declares `module github.com/victorzhuk/gohan/adapter/otel`, `go 1.27.1`, requiring `github.com/victorzhuk/gohan v0.1.0` plus the `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/metric`, `go.opentelemetry.io/otel/sdk`, `go.opentelemetry.io/otel/sdk/metric` and `go.opentelemetry.io/otel/trace` modules (v1.38.0); `adapter/otel/otel.go:1-30` is package `otel` with one exported type and a `New` constructor. The workspace file `go.work` lists `.`, `./adapter/otel` and `./examples`.

Anthropic's concrete mapping is documented in `docs/design/adapters.md:61`: `CacheExplicit` with `CacheBreakpoints: 4`, `CacheBreak` to `cache_control`, `CacheWriteTokens` from usage, `ResponseSchema` to a forced tool schema, opaque `Reasoning` with signature round-trip, tool use, streaming. The `model` spec does not state this mapping, so one requirement is added (`specs/model/spec.md`).

## Goals and non-goals

**Goals:** Deliver a `types.Model` Anthropic adapter that passes the shared suite's eight registered cases over recorded fixtures; prove public Send behaviour for `Extra` collision, schema refusal, cache markers, `Retry-After` propagation, `Raw` fidelity and key secrecy; add no second ID for a behaviour the suite already claims.

**Non-goals:** No `Decider` in this change. The milestone phrase "Model/Decider" describes the module's eventual content; no `decider.*` scenario is assigned to this change (`openspec/scenarios.json:1563-1582` carries no `deferred_to`), so the decider lane is out of scope here. No new capability, no change to `core` or `std`, no release tag, no network or credential use in tests.

## Decisions

### D1 — Module and package shape

Create `adapter/anthropic` as a separate Go module `github.com/victorzhuk/gohan/adapter/anthropic` at `go 1.27.1`, requiring `github.com/victorzhuk/gohan v0.1.0`, mirroring `adapter/otel/go.mod`. Package `anthropic` exports `New(cfg Config) *Model` and `Model` implementing `types.Model` (`core/types/model.go:11`); `Config` carries the endpoint, the credential, the `types.ModelProfile` and an `*http.Client`. The package keeps `core` free of any Anthropic type, exactly as `adapter/otel/otel.go:1-4` keeps OpenTelemetry out of the floor.

### D2 — Request translation and dynamic collision

`Generate` builds the Anthropic Messages body from `ModelRequest` (`core/types/model_request.go`): `System` to the top-level `system`, `Messages` per block, `Tools` to the Anthropic tool schema, and `Options` to `max_tokens`/`temperature`. Before any request, a key of `Options.Extra` present in `types.GovernedModelOptionKeys()` (`core/types/model_request.go:29`) fails with `&types.ModelError{Class: ClassPermanent, ...}` naming the key; every other `Extra` key is copied into the body as an extension. This is the call-time duty the conformance contract leaves to the adapter (`m1-provider-conformance-contract/design.md` D4).

### D3 — Error normalization and version reporting

A non-2xx response maps through `types.ClassifyProviderError(status, code)` (`core/types/model_profile.go:132`) into `types.ModelError` (`core/types/errors.go:180`); the `Retry-After` header is parsed to `RetryAfter` so the class-429 fixture's `7s` is preserved. When `Caps.StrictVersion` holds and the served version differs from `Profile.Version`, the call fails `ClassVersionDrift` directly, because that class is not derivable from a status (`core/types/model_profile.go:129`).

### D4 — Cache markers and schema

`CacheBreak` blocks are emitted as ordered `cache_control` markers, capped at `Caps.CacheBreakpoints` (`core/types/model_profile.go:58`); `Usage.CacheWriteTokens` is filled from the Anthropic usage block. A `ResponseSchema` is honoured by forcing a single tool carrying the schema when `Caps.Constrained` is true, and refused with `ClassPermanent` before any request when it is false — which is what `assertSchemaFixture` (`testkit/conformance/model.go:195`) requires.

### D5 — Recorded fixtures and the eight IDs

The adapter ships recorded Anthropic wire responses as data and a fixture-backed `types.Model` that runs the adapter's real decoding path over them; no network is used. Its `TestModelConformance` calls `conformance.ModelScenarios(t, newModel, anthropic.Fixtures())` (`testkit/conformance/model.go:49`) so the subtest names are the eight registered IDs. `FixtureKey` (`testkit/conformance/fixtures.go:58`) marks a request as playback. The adapter reports each ID once; it adds no second ID for the same behaviour.

### D6 — Public Send proofs

A separate `TestAnthropicSend` drives the public `Generate` path against a stub transport and asserts: dynamic `max_tokens` collision returns `ClassPermanent` before send; an unsupported schema sends nothing; the marker payload is ordered and capped; `Retry-After: 7` reaches `ModelError.RetryAfter`; a `Raw` block round-trips unchanged to the same provider and is dropped for another; and a sentinel token created at test time appears in no span attribute, log line, error string or recorded cassette, while `types.KeyModelKeyID` (`core/types/telemetry.go`) names the credential. Secrets are never stored in fixtures.

### D7 — Module wiring

Add `./adapter/anthropic` to `go.work` and to the explicit module lists in `Taskfile.yml`: `spec:coverage` (`:20`) and `spec:gate` (`:32`) get the coverage run, and `test` (`:70`) and `test:race` (`:82`) get the plain and race runs, so the new module is exercised by the repository gates. `api:check` iterates `adapter/*` and skips an untagged module (`Taskfile.yml:48-58`), so no baseline is needed.

### D8 — Dependencies

`m1-provider-conformance-contract` supplies `ModelScenarios`, `FixtureKey` and the fixture kinds; its tasks 3.2 and 3.3 are the obligations implemented here. `m1-provider-metadata-contract` supplies the request-metadata field, `ModelRequest.Metadata` with `RequestMetadata{CostTags}` — `CostTags` only; `AffinityKey` and `Priority` are `ModelOptions` fields (`core/types/model_request.go:19-28`). Until the field lands, cost-tag forwarding stays adapter-local and are recorded as prerequisites, not as shipped bindings.

## Risks

- The eight IDs stay unearned unless the recorded-fixture harness exercises the real decode path rather than a hand-written double; D5 pins that.
- `assertSchemaFixture` (`testkit/conformance/model.go:195`) and the forced-tool emulation (D4) must agree on `Caps.Constrained`: a profile with emulation but without the capability fails the suite.
- The module is untested by `task test` until D7 lands, and by `spec`/`spec:gate` for the same reason.
