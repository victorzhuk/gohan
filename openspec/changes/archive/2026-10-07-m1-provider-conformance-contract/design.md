# Design: m1-provider-conformance-contract

## Context

The M1 acceptance matrix assigns this change all ten IDs in its model row except `model.provider-tool-usage`, which belongs to `m1-provider-tool-governance`. However, the sealed assignment decision restricts registered coverage here to the two Build-actor IDs, `model.extra-collision` and `model.undeclared-fidelity`. The other eight IDs have adapter actors. Their owners are `m1-openai-provider` and `m1-anthropic-provider`, which must run this shared suite against recorded provider responses and use the exact scenario IDs as subtest names.

The normative requirements are `openspec/specs/model/spec.md` lines 295–364: Provider adapter contract and Provider keys. Do not alter those sealed behaviors. Tenant key selection already has types in `core/types/model_keys.go`: `ProviderCredential`, `ProviderKeySource.ProviderKey(ctx, profile, tenant)`, and `ProviderKeyValidator.Validate(ctx, profile, credential)`. `types.ModelProfile` in `core/types/model_profile.go` includes `Keys`, `Caps`, and `Name`. `types.ModelRequest` in `core/types/model_request.go` includes `Options`; `ModelOptions.Extra` is a `map[string]any`.

## Goals and non-goals

**Goals:** Define deterministic shared fixtures and reusable test entry points. Specify Build validation for unsupported fidelity and static governed-field collisions. Give later adapters executable suite contracts.

**Non-goals:** Implement adapters, provider networking, runtime key selection, breaker isolation, telemetry sanitization, key storage, cache translation, or model option assembly. Do not count unit/reference-double results as adapter scenario coverage.

## Decisions

### D1 — Suite ownership and IDs

Keep provider-owned behavior tests in `testkit/conformance`, but expose adapter runner entry points whose subtests use the exact scenario IDs. The eight adapter IDs remain unearned until each provider lane runs its recorded-response cases. Each adapter lane MUST report its own ID results. The reference double proves suite sensitivity only; it does not earn adapter coverage.

The two IDs earned in this change are `model.undeclared-fidelity` and `model.extra-collision`. The fidelity test uses public `Build` with a tool returning `types.KindFile` and a profile whose `Caps.Fidelity` omits `KindFile`. The collision test uses public `Build` with a static `ModelOptions.Extra` containing `max_tokens`. Dynamic collision behavior remains a provider-call contract and cannot be earned by Build-only validation.

### D2 — Fixture inventory and kinds

Retain existing `FixtureError`, `FixtureUsage`, `FixtureTruncation`, and `FixtureRawRoundTrip`. Add fixture kinds only when they represent a different input/observation shape. The fixture set for the ten assigned behaviors is:

| Behavior | FixtureKind | Status |
|---|---|---|
| Status 429/500/401/400/context-length/retired | `FixtureError` (existing), six distinct fixtures | recorded-response adapter test |
| Retry-After 7 seconds | `FixtureError` (existing), 429 fixture with `RetryAfter: 7*time.Second` | recorded-response adapter test |
| Three cache breaks, cap two, usage cache-write | new `FixtureCacheBreakpoints` | recorded-response adapter test |
| Schema without constrained capability | new `FixtureSchemaUnsupported` | adapter preflight test; zero requests asserted |
| Missing usage estimate | `FixtureUsage` (existing), `Usage == nil` | recorded-response adapter test |
| Raw provider round trip and cross-provider drop | `FixtureRawRoundTrip` (existing), provider and target provider fields | recorded-response adapter test; exact outgoing payload asserted by adapter harness |
| Per-key breaker isolation | new `FixtureKeyIsolation` | two-tenant adapter integration harness |
| No key secret in telemetry/audit/cassette/error | new `FixtureKeyRedaction` | adapter integration harness with sentinel token |
| Static and dynamic `Extra` collision | new `FixtureExtraCollision` | Build and provider-call harness; static part is earned here only after public Build test |
| Missing fidelity for possible output kind | new `FixtureFidelity` | public Build test; earned here |

Add a `Fixture` field for structured inputs only when needed by these cases: `TargetProvider string`, `CacheBreaks int`, `MaxBreakpoints int`, and `ExtraField string`. Do not store secret tokens in fixtures. The key-redaction harness creates a sentinel token at test time.

### D3 — Reference double and suite entry points

Place the test-only reference double in `testkit/conformance/model_test.go` beside `fixtureModel`, `swallowModel`, and `captureTB`. The double replays `Fixture` without network access. Negative controls use the existing `conformanceTB`/`captureTB` seam and assert `captureTB.Failed()`.

The existing adapter entry point is `conformance.Model(t *testing.T, newModel func(Fixture) types.Model, fixtures Fixtures)` in `testkit/conformance/model.go`. Preserve this signature. Add exported `ModelScenarios(t *testing.T, newModel func(Fixture) types.Model, fixtures Fixtures)` in that package. It runs adapter behavior under exact scenario-ID subtest names. Add `BuildScenarios(t *testing.T)` for Build-owned scenarios; it seeds public `Build` options and does not claim adapter coverage. Adapter packages call `conformance.ModelScenarios(t, newModel, fixtures)` from their `TestModelConformance` entry point. The reference double and fixture tests prove suite sensitivity only.

### D4 — Build registration and validation

ADR-0157 §8 settles the static source: add `ModelOptions types.ModelOptions` to `NativeSpec` in `core/native_options.go`; register it through `WithNativeAgent(spec NativeSpec) Option`. In `core/native_resolve.go`, `Stack.resolveNative` validates the registered `spec.ModelOptions.Extra` against the governed request fields before Build succeeds. A collision returns a Build error that names the governed field. Per-request `ModelRequest.Options.Extra` remains an adapter call-time check and returns a permanent `ModelError` (`ClassPermanent`); no live adapter Send function exists yet, so each adapter lane must name its concrete Send function when authored.

The exact existing fidelity validation site is `core/native_resolve.go`, `Stack.resolveNative`, at `s.CheckFidelity(req, profiles, spec.Profile)`. `Stack.CheckFidelity(req FlowRequest, profiles map[string]types.ModelProfile, primary string) error` is defined in `core/build_fidelity.go`; missing fidelity wraps `ErrFidelityUndeclared` and identifies flow, profile, and block kind. Seed `model.undeclared-fidelity` through public `Build` using `WithNativeAgent(NativeSpec{Request: FlowRequest{Blocks: []types.BlockKind{types.KindFile}}, Profile: ...})` and a profile whose `Caps.Fidelity` omits `KindFile`. Seed `model.extra-collision` through public `Build` using `WithNativeAgent(NativeSpec{ModelOptions: types.ModelOptions{Extra: map[string]any{"max_tokens": ...}}})`; assert Build refusal names `max_tokens`, before any model call.

### D5 — Coverage boundary and key telemetry

This package earns only the two Build-actor IDs, `model.undeclared-fidelity` and `model.extra-collision`. The eight adapter-actor IDs remain unearned until both `adapter/openai` and `adapter/anthropic` run the shared `conformance.Model` suite through `ModelScenarios` with recorded responses. Do not claim `model.isolation-per-key` or `model.key-never-in-telemetry` here: `ProviderKeySource` is defined in `core/types/model_keys.go`, and `types.KeyModelKeyID` is defined in `core/types/telemetry.go`, but no emitter exists. Both remain adapter-lane obligations.

Fixture seeding by behavior:

| Behavior | FixtureKind | Public behavior proof |
|---|---|---|
| Status classes and Retry-After | `FixtureError` | Recorded provider response; distinct 429, 500, 401, 400, context-length, and retired fixtures |
| Cache breakpoints and cache-write usage | `FixtureCacheBreakpoints` | Recorded response plus exact outgoing payload and usage |
| Schema unsupported | `FixtureSchemaUnsupported` | Public adapter Send; assert zero requests |
| Missing usage estimate | `FixtureUsage` | Recorded successful response with `Usage == nil` |
| Raw round trip and cross-provider drop | `FixtureRawRoundTrip` | Public Send; assert outgoing payload and drop accounting |
| Per-key breaker isolation | `FixtureKeyIsolation` | Two-tenant adapter harness |
| Key secret exclusion | `FixtureKeyRedaction` | Adapter harness creates sentinel credential at runtime |
| Static/dynamic Extra collision | `FixtureExtraCollision` | Public Build for static key; adapter Send for per-request key |
| Missing fidelity | `FixtureFidelity` | Public Build with omitted `KindFile` fidelity |

Existing kinds are `FixtureError`, `FixtureUsage`, `FixtureTruncation`, and `FixtureRawRoundTrip` in `testkit/conformance/fixtures.go`. The named new kinds are required additions there. Do not place secret tokens in fixtures.

### D6 — Strict test evidence

Use `existing-service-strict`. Deterministic fixture-boundary tests are required. The two earned IDs require public Build evidence. Adapter-owned scenarios require recorded-response execution through each adapter's public `types.Model.Generate` path. Reference-double and unit-only results do not establish adapter scenario coverage. Use no external network or credentials.

### D7 — Sealed decisions

Preserve `openspec/changes/m1/design.md` §3. This package does not alter taint, egress, permission, or key policy. Keep root pre-v1 through M2, HTTP comparison in M4, and no release tags. Provider transport configuration is distinct from model-directed network access.

## Blocker resolution (live seams)

Task 4.1 names the four live facts the design wrote against. Measured on the tree, 2026-10-07:

1. **Static and dynamic `Extra`.** Static: `NativeSpec.ModelOptions types.ModelOptions` (`core/native_options.go:21`), validated in `Stack.resolveNative` (`core/native_resolve.go:59-61`) against `types.GovernedModelOptionKeys()` (`core/types/model_request.go:29-41`, nine keys: `model`, `messages`, `tools`, `tool_choice`, `max_tokens`, `temperature`, `response_format`, `output_schema`, `stream`). The static value is validated and then dropped: nothing merges it into the assembled `ModelRequest`, which is the missing step this change adds. Dynamic: `ModelOptions.Extra` (`core/types/model_request.go:26`) is produced per request at assembly (`core/drive_turn.go:253` via `c.assemble`); no call-time collision check exists anywhere, so a dynamic governed key is the adapter's `ClassPermanent` duty at the call boundary, exactly as D4 states. Serialization: `ModelRequest.MarshalJSONTo` (`core/types/model_request.go:113-122`) writes `Options` verbatim into the frozen wire order the record/replay key hashes (`testkit/gohantest/cassette.go:244-248`); no provider payload serializer exists in the repository.
2. **Error representation.** `types.ModelError{Class, Provider, Status, Code, RetryAfter, Err}` (`core/types/errors.go:179-186`) over `ErrorClass` (`:166-177`), `Retryable()` true only for `ClassRateLimited` and `ClassTransient` (`:197-199`). `ClassifyProviderError(status, code)` (`core/types/model_profile.go:132`) is the single classifier and has no live caller yet; the problem catalog maps `retry_after` from `ModelError.RetryAfter` (`core/types/problem.go:225-256`).
3. **Key telemetry emission site.** `KeyModelKeyID` is declared and never emitted. The emission surfaces are the span helpers (`startSpan`, `telemetryFrom`) and the structured logger; no path puts key material in them today, so the suite asserts absence rather than redaction of a leak.
4. **`NativeSpec` and `ModelOptions`.** `NativeSpec` (`core/native_options.go:16-22`) carries `ModelOptions` and `Assemble`; `Stack.resolveNative` binds the model chain and rejects a static governed key before `Build` succeeds. The nine governed keys above are the frozen list; `KindFile` fidelity is a separate obligation of the assembled request's block kinds.

**Adapter reality (blocks section 3).** `adapter/` holds only `otel`. `adapter/openai` and `adapter/anthropic` do not exist, so tasks 3.1–3.3 — recorded fixtures and public Send behaviour in each real adapter — cannot land inside this change. They stay open and unstarted, owned by the provider-adapter work that introduces those modules; the suite, fixtures and reference doubles this change lands are what those adapters will run.

## Open questions

- Adapter implementation packages and their public Send function names do not exist yet. This blocks exact per-request `Extra` validation call sites and key telemetry-emission call sites; each adapter design must name them before implementation admission.
- The driver pins the governed inventory and the error. `core/types` declares the set once beside `ModelOptions` — the provider keys the harness maps from it on every call, `max_tokens` among them — and `Build` refuses a static `NativeSpec.ModelOptions.Extra` key in that set with `types.ErrExtraGovernedKey{Key string}`, mapped to the build problem the way `ErrToolCollision` is. An adapter reuses the same set for the per-request check and returns `ClassPermanent` naming the key. The adapter call sites themselves stay open until the adapter packages exist, and those packages own them.

## Risks

- Adapter-owned recorded-response harnesses do not exist yet. The eight adapter-actor IDs remain unearned until both provider lanes execute the shared suite.
- The current `Model` suite uses fixture-name subtests. `ModelScenarios` must expose scenario-ID subtests without changing coverage ownership.

