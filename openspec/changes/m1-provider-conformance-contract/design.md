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

Place the reference double in `testkit/conformance/model_test.go` beside existing `fixtureModel`, `swallowModel`, and `captureTB`. Keep it test-only. Extend its request capture so tests can inspect provider request values, endpoint attempts, returned Raw data, and telemetry-like outputs. It replays `Fixture` values deterministically and performs no network access.

Existing adapter entry point is `conformance.Model(t *testing.T, newModel func(Fixture) types.Model, fixtures Fixtures)` in `testkit/conformance/model.go`. Add an exported `ModelScenarios` entry point for adapter packages. It accepts the testing handle, fixture-to-model factory, and recorded fixture set, then emits exact scenario-ID subtests. Add a separate `BuildScenarios` entry point for the two Build-owned checks. The concrete signature remains BLOCKED until the existing conformance `conformanceTB` interface is exported or the wrapper stays entirely inside this package. Prefer keeping the adapter API test-only and accepting `*testing.T`; define exact signature during implementation planning after confirming downstream adapter package import/test visibility. Do not expose a placeholder API.

The existing `conformanceTB` and `captureTB` are the negative-proof seam. Add a negative control that wraps the reference double with a deliberate violation, runs the relevant scenario against `captureTB`, and asserts `captureTB.Failed()` is true. Existing proof at `model_test.go:30-39` demonstrates swallowed-error sensitivity. Keep scenario-level failures isolated so the outer Go test passes when it observes the expected failure.

### D4 — Build and Extra call sites

Fidelity validation already runs in `Stack.resolveNative` at `core/native_resolve.go:76`, through `s.CheckFidelity(req, profiles, spec.Profile)`. The check receives `req.Tools` from `toolSpecs(spec.Tools)` at line 56 and the resolved profile. The implementation must add a negative fixture through public `Build` and prove the error names `profile.Name` and `types.KindFile`. The exact location of static `ModelOptions.Extra` assembly is unresolved: live core has `ModelOptions.Extra` in `core/types/model_request.go`, but no provider adapter assembles provider JSON and no static-versus-dynamic request-value distinction exists. Do not claim the design pins a call site. This blocks implementation admission for that part until the live option registration and assembly path are identified.

The matrix scenario says Build rejects a static value and a dynamic value fails permanently at call time. No current `Build` option or adapter call site represents dynamic `Extra`; no adapter exists. The package specifies the contract only. Later implementation plans must name exact option types, validation function, error value, and provider Send call site. No coder may choose these APIs without that evidence.

### D5 — Key privacy and telemetry boundary

`types.KeyModelKeyID` is defined as `gohan.model.key_id` in `core/types/telemetry.go:73`. No live provider adapter emits it. `ProviderKeySource` and `ProviderCredential` exist in `core/types/model_keys.go`, but there is no consumer yet. The adapter implementation must attach only credential `ID` to the model-call span and must exclude `Token` from spans, audit records, cassette serialization, and errors. This design cannot name a live emission function because none exists. Record exact emission call site as an implementation-admission blocker until each adapter implementation identifies its Send function and credential boundary.

### D6 — Strict test evidence

Use `existing-service-strict`. Deterministic fixture boundary tests are required. The two earned Build IDs require public `Build` evidence. Adapter-owned tests require recorded-response calls through each adapter's public `types.Model.Generate` path. Unit-only checks and the reference double cannot establish adapter coverage. Add no external network or credentials.

### D7 — Sealed decisions

Preserve the M1 decisions in `openspec/changes/m1/design.md` §3. This package does not alter taint, egress, permission, or key policy. Keep root pre-v1 through M2, HTTP comparison in M4, and no release tags. Provider transport configuration is distinct from model-directed network access.

## Risks and blockers

- The adapter suite scenarios need adapter-owned captured-response harnesses. They do not exist today.
- `ModelOptions.Extra` has no live adapter serialization point. Static/dynamic collision implementation cannot enter a coder lane until that point and its value source are named.
- No provider key consumer or model span emission site exists. Key isolation and redaction remain unverified until adapter changes supply those public paths.
- The current `Model` suite fixture subtests use descriptive names. The future adapter wrapper must use scenario IDs without claiming registered coverage in this package.
