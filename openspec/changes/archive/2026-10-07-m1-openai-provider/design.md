# Design: m1-openai-provider

## Context

`adapter/` holds only the `otel` module (`adapter/otel/go.mod:1`), which is the layout this change copies: a separate Go module whose package imports the root module and is wired into `go.work` and `Taskfile.yml`. No provider adapter exists, so the shared suite `conformance.ModelScenarios` (`testkit/conformance/model.go:49`) and its fixture set (`testkit/conformance/fixtures.go:53-56`) have no real consumer yet.

This change implements the *Provider adapter contract* (`openspec/specs/model/spec.md:265-337`, rules 1-9) and *Provider keys* (`:339-360`) for an OpenAI chat-completions endpoint. The error surface is `types.ModelError` (`core/types/errors.go:179-199`) built from `types.ClassifyProviderError` (`core/types/model_profile.go:132`). The port is `types.Model` (`core/types/model.go:11-13`). The request wire shape is `ModelRequest.MarshalJSONTo` (`core/types/model_request.go:113-122`).

`m1-provider-conformance-contract` seals the ownership: the eight `model.*` adapter IDs stay unearned until `adapter/openai` and `adapter/anthropic` run the shared suite; an adapter-specific requirement "lands in that adapter's own design without claiming a second ID for the same behaviour" (`openspec/changes/m1-provider-conformance-contract/design.md` D1 and `openspec/changes/m1/design.md` §2.4). Its tasks 3.1/3.3 are the obligations this change implements (`openspec/changes/m1-provider-conformance-contract/tasks.md:27-29`).

## Goals / Non-Goals

**Goals:** Ship a self-contained `adapter/openai` module that passes the shared conformance suite and the task 3.3 public `Send` proofs, with recorded fixtures and no network or credentials in the default test run.

**Non-Goals:** The Anthropic adapter (`m1-anthropic-provider`), the provider-executed tool path (`m1-provider-tool-governance`), hedging/retry/breaker wiring (`m1-hedged-model-calls`, `std/route`), the request-metadata field itself (`m1-provider-metadata-contract`), and any `openspec/specs/` edit or release tag. Request assembly targets the OpenAI chat-completions shape only.

## Decisions

### D1 — Module layout, public API and the skip_specs boundary

New module `github.com/victorzhuk/gohan/adapter/openai`, `go 1.27.1`, requiring the root module, added to `go.work` beside `./adapter/otel`. Package `openai`, files `openai.go` (types, `New`, `Profile`, `Generate`), `request.go`, `stream.go`, `errors.go`.

Public surface:

```go
type Config struct {
    Profile   types.ModelProfile
    Endpoint  string        // base URL; default https://api.openai.com/v1
    Transport Transport     // nil → http.DefaultTransport
    Keys      types.ProviderKeySource
    AffinityHeader string   // header Extra/AffinityKey mapping; empty → no header
    Now       func() time.Time
}
type Transport interface { Do(*http.Request) (*http.Response, error) }
type Provider struct { … }
func New(cfg Config) (*Provider, error)
func (p *Provider) Profile() types.ModelProfile
func (p *Provider) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error]
```

`New` validates that the profile declares streaming and a fidelity entry for every modelled block kind it must preserve, and that `Caps.Cache`/`Caps.CacheBreakpoints` are consistent; a bad profile fails `New`, never a call.

`skip_specs: true` is correct because every behaviour earned here is already stated normatively: the adapter does not introduce a requirement, it satisfies `openspec/specs/model/spec.md:265-337` and `:339-360`. The OpenAI-specific wire mapping below is implementation, and the sealed decision (`m1-provider-conformance-contract` design D1) puts an adapter-specific requirement in *this* design, not in a second spec delta. The package therefore writes proposal/design/tasks only.

### D2 — Request assembly (`request.go`)

Map `ModelRequest` to a chat-completions body in one pure function `assembleBody(req) (map[string]any, error)`:

- `req.System` blocks → a leading `{"role":"system","content":…}` message; `req.Messages` → role messages; `Text` → string content, `Reasoning` → dropped on re-send (assistant reasoning is not replayed), `ToolUse` → `tool_calls`, `ToolResult` → `{"role":"tool",…}`.
- `req.Tools` → `tools` with `function.name/description/parameters`; `ToolChoice` (`auto`/`none`/`any`) → `tool_choice`.
- `Options.MaxTokens` → `max_tokens`; `Options.Temperature` → `temperature`; `Options.Priority` → the engine priority field inside `extra_body` where the endpoint supports it, else ignored with a `Build`-time warning path (documented, not a call error).
- `Options.Extra` → top-level body keys, minus the governed set; see D4.
- `AffinityKey` → the configured `AffinityHeader` value; empty header name → no header.

The body is the fixture transport's input (D8), so assembly is proven by the conformance run rather than by a private unit test alone.

### D3 — Streaming, usage and finish (`stream.go`)

Read `text/event-stream`. Each `data:` line is a chunk; `[DONE]` ends the stream. Map `choices[].delta.content` → `ModelChunk{Kind: DeltaText}`, `delta.reasoning_content`/`reasoning` → `DeltaReasoning`, `delta.tool_calls` accumulated by index → a complete `ModelChunk{ToolUse: …}` once the arguments close (rule 6: `ToolUse` blocks are emitted complete). `finish_reason` → `FinishReason` (`stop`→`FinishStop`, `tool_calls`→`FinishToolUse`, `length`→`FinishMaxTokens`, `content_filter`→`FinishRefusal`).

`usage` is emitted on the final chunk only (rule 5): `prompt_tokens`→`InputTokens`, `completion_tokens`→`OutputTokens`, `prompt_tokens_details.cached_tokens`→`CachedInputTokens`, `CacheWriteTokens`→D6; `ModelVersion` is the served `model`; `KeyID` is the resolved credential id (D7). When the provider sends no `usage`, the adapter sets `Estimated = true` and fills `OutputTokens` (and `InputTokens`) from the `std/tokens.Heuristic` byte estimate over the assembled request and the streamed deltas — never reporting zero. This is the `model.usage-estimated` behaviour (`openspec/specs/model/spec.md:319-322`; the Usage rule is `:273`).

The iterator body runs on the caller's goroutine, returns within one read of `ctx.Done()`, closes the response body on early `yield` return, and yields nothing after an error (rule 6 iterator contract, `openspec/specs/model/spec.md:287`).

### D4 — `Extra` collision (task 3.3)

`Options.Extra` keys are merged into the body at top level. A key in `types.GovernedModelOptionKeys()` (`core/types/model_request.go:29-41`) is refused before any request: `Generate` yields `&types.ModelError{Class: types.ClassPermanent, Provider: profile.Name, Code: "gohan.extra_governed_key"}` naming the key, with no transport call. The static half of `model.extra-collision` is `Build`-owned and earned by `m1-provider-conformance-contract`; this adapter earns only the dynamic, call-time half (rule 7; `openspec/specs/model/spec.md:275`).

### D5 — Structured output / schema refusal (task 3.3)

`Options.ResponseSchema` non-empty on a profile with `Caps.Constrained` → `response_format: {"type":"json_schema","json_schema":{"name":…,"strict":true,"schema":…}}`. On a profile without `Caps.Constrained` the call fails before sending with `ClassPermanent` (`Code: "output_schema_unsupported"`), never streaming unconstrained content (rule 4; `openspec/specs/model/spec.md:272`). This earns `model.schema-never-ignored` (`:315`).

### D6 — Cache (task 3.3, `model.cache-explicit-breakpoints`)

Behaviour follows the profile's `Caps.Cache` (rule 3; `openspec/specs/model/spec.md:271`):

- `CacheNone`: `CacheBreak` blocks dropped; nothing added.
- `CacheAuto`: `CacheBreak` dropped, `CachedInputTokens` filled from the provider usage.
- `CacheExplicit`: the first `Caps.CacheBreakpoints` `CacheBreak` blocks map to provider cache markers in order, later ones are dropped and counted on `gohan.cache.breakpoints_dropped`; `CacheWriteTokens` is filled from usage.

The conformance cache fixture (`FixtureCacheBreakpoints{CacheBreaks:3, MaxBreakpoints:2}`) is replayed against a model whose profile declares `CacheExplicit` and `CacheBreakpoints: 2`; the suite's `assertCacheFixture` then requires `CacheWriteTokens > 0` on the final chunk (`testkit/conformance/model.go`, `assertCacheFixture`). The marker payload is emitted only for endpoints that declare `CacheExplicit`; the design of `docs/design/adapters.md:56-58` records OpenAI-compatible prefix caching as `CacheAuto`, and this change treats `CacheExplicit` as a profile-declared endpoint capability (vLLM/enterprise gateways) rather than a new default — an implementation prerequisite is that the conformance profile declares it. `docs/design/adapters.md` §7.5 is reconciled at apply time.

### D7 — Provider keys and key secrecy (task 3.3, `model.isolation-per-key`, `model.key-never-in-telemetry`)

`Generate` resolves the credential once per call from `cfg.Keys.ProviderKey(ctx, profile.Name, tenant)` with `tenant` from `types.PrincipalFrom(ctx)` (`core/types/identity.go:11,136`), never from an argument. `KeyMode` selects the tenant/platform argument per `openspec/specs/model/spec.md:339-341`. `Usage.KeyID` is the resolved `ProviderCredential.ID` (`core/types/model_keys.go:8-12`). No credential is cached across calls or tenants, so a per-key breaker keyed by `(profile, credential.ID)` cannot leak isolation.

The token is never written into an error, a log line, a span attribute, a recorded request, or a cassette: the only telemetry identity is `types.KeyModelKeyID` = `gohan.model.key_id` (`core/types/telemetry.go:73`), carrying the credential id, never the token. `ProviderKeyValidator` (`core/types/model_keys.go:19-21`) is honoured when the source implements it.

### D8 — Recorded fixtures and the conformance harness

The adapter ships `testdata/*.json` recordings for every fixture in `conformance.DefaultFixtures()` (`testkit/conformance/fixtures.go:64-168`), one per fixture, holding the exact provider body/SSE the fixture describes (a 429 with `Retry-After: 7`, the `insufficient_quota` 400, 500, 401, 400, the context-length 400, the retired 404, a usage response, a no-usage response, a truncated tool call, the unmodelled-block round trip, the cache response, and the schema-unsupported refusal).

The conformance test file (`conformance_test.go`, package `openai_test`) builds `newModel func(conformance.Fixture) types.Model`: it constructs a `Provider` whose `Transport` is a `fixtureTransport` that reads the outgoing request body, finds the `gohan.conformance.fixture` key that assembly copied from `Options.Extra`, and returns the recording for that name. This drives the adapter's public `Generate` path with no network and no credentials, and proves request assembly as a side effect.

`TestModelConformance(t)` calls `conformance.ModelScenarios(t, newModel, fixtures)`; the suite creates one subtest per registered ID (`testkit/conformance/model.go:57-63`).

Two IDs need explicit handling. The suite's `runFixture` switch has no assertion case for `FixtureKeyIsolation` or `FixtureKeyRedaction` (`testkit/conformance/model.go:140-160`), so the subtests `ModelScenarios` creates for `model.isolation-per-key` and `model.key-never-in-telemetry` are empty. Those two behaviours are proved in dedicated tests (`TestProviderKeyIsolation`, `TestKeyNeverInTelemetry`) whose subtests carry the exact IDs. Because the coverage gate matches by the last subtest segment (`tools/spec_coverage.py:48-56`), a real subtest under a different parent supplies the ID and does not collide with the suite's subtests. Both tests use a `ProviderKeySource` double and a sentinel token, and assert: distinct tenants resolve distinct credentials and the `t1` 429 does not change `t2`'s outcome; and the sentinel token appears in no error string, request capture, span attribute or log line.

### D9 — Repository gate wiring

`go.work` gains `./adapter/openai`. `Taskfile.yml` gains `go -C adapter/openai test -json -timeout 2m -short ./...` in `spec:coverage` and `spec:gate` (`Taskfile.yml:20,32`), and `go -C adapter/openai test -timeout 2m -short ./...` / `-race` in `test` and `test:race` (`:70,82`); `api:check` already globs `adapter/*`. Without the `spec:coverage`/`spec:gate` line the adapter's subtests never reach the coverage stream and the eight IDs stay missing in scope, so this wiring is part of the acceptance, not optional.

### D10 — Dependency on `m1-provider-metadata-contract`

Cost-tag forwarding (`lifecycle.cost-tags-forwarded`, `openspec/changes/m1/design.md` §2.10) needs a request-metadata field on the provider contract, which `m1-provider-metadata-contract` will introduce. That package already carries `proposal.md`, `design.md`, `tasks.md` and two spec deltas (`specs/model/spec.md`, `specs/lifecycle/spec.md`), so this change consumes its field: `ModelRequest.Metadata` with `RequestMetadata{CostTags}` — `CostTags` only. The adapter leaves a single seam: `Config` accepts an optional metadata injector once the field exists, and until then no cost tag reaches the provider. Recorded as an implementation prerequisite, not as shipped forwarding.

## Risks / Trade-offs

- **OpenAI wire specifics have no recorded ground truth in the repo** → Mitigation: the recordings are authored fixtures that state the exact provider bytes; the design names each field and the spec rule it satisfies, so a later correction is a fixture edit, not a contract change.
- **`CacheExplicit` for OpenAI conflicts with `docs/design/adapters.md:56-58`** → Mitigation: the adapter does not change OpenAI's default; `CacheExplicit` is profile-declared, `docs/design/adapters.md` §7.5 is reconciled at apply time, and the shared cache fixture's profile declares the mode it asserts.
- **The suite asserts nothing for two of the eight IDs** → Mitigation: D8 puts the real proofs in dedicated ID-named tests; the suite gap is recorded, not hidden.
- **`go.work`/`Taskfile.yml` edits touch shared files** → Mitigation: additive lines only; no existing module entry is changed.
- **No test run is in this package's scope** → Mitigation: the design fixes the exact file layout, API and fixture transport so the coder can implement and the suite can run without re-deciding.

## Migration Plan

Additive. Land `adapter/openai`, its `testdata`, the conformance tests, then the `go.work` and `Taskfile.yml` lines in the same change so the gate sees the new module. Rollback is deleting the module and its two wiring lines; no shipped code path changes.

## Open Questions

- Whether the metadata field from `m1-provider-metadata-contract` is a header or a body key is deferred to that package; the adapter's `Config` seam is agnostic.
- The exact vLLM/enterprise priority field name is deferred to implementation and does not change any spec or task.
