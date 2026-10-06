# Tasks

## 1. Authoring

- [x] 1.1 Record the five assigned `lifecycle` IDs, their milestone status and the two dependency boundaries (provider metadata, `m1-exact-match-cache`). Evidence: `proposal.md` Capabilities and Impact; `design.md` Context and D5.
- [x] 1.2 Add the `lifecycle` delta with the complete "Retirement, schema evolution, cost" requirement, its seven registered scenarios verbatim, and the new normative text. Evidence: `specs/lifecycle/spec.md`.
- [x] 1.3 Name the new metrics in the requirement prose and mark them as implementation-emitted. Evidence: `specs/lifecycle/spec.md` (Requirement prose: `gohan.model.deprecated_calls`, `gohan.model.deprecated_routed`, `gohan.model.sunset_warning`).
- [x] 1.4 Validate this package. Evidence: `openspec validate m1-model-retirement-cost --strict --no-interactive` → valid.

## 2. Notice window and deprecation routing

- [x] 2.1 Add the notice-window and warn-only options to `Build` (`core/build_options.go`), default 30 days, reading `ModelProfile.Sunset`/`Successor` (`core/types/model_profile.go:13-14`) and passing a zero `Sunset`. Verify: `go test -short -timeout 2m ./core/... -run 'TestBuildSunset'` fails a 20-day profile, warns under the warn-only option, and passes a zero `Sunset`.
  - evidence: `core/build_options.go` (`WithSunsetNoticeWindow`, `WithSunsetWarnOnly`, `DefaultSunsetNoticeWindow`), `core/deprecated.go` (`checkSunsets`); `core/build_sunset_test.go` (`TestBuildSunset`, subtest `lifecycle.sunset-inside-notice-window`).
- [x] 2.2 Emit `gohan.model.sunset_warning{profile}` on the warn path and `gohan.model.deprecated_calls{profile}` / `gohan.model.deprecated_routed{profile,successor}` on the deprecation path. Verify: metric assertions in the same test file.
  - evidence: `core/deprecated.go` emits all three through `types.Telemetry`; `std/telemetry/metrics.go` declares them and allowlists the `successor` label; `std/telemetry/metrics_test.go` (`lifecycleMetricRegistration`).
- [x] 2.3 Route a `ClassDeprecated` call to `Successor` when declared and otherwise return `*ModelError{Class: Deprecated}`, with no fallback assistant message. Verify: `go test -short -timeout 2m ./core/... -run 'TestDeprecatedNeverSilent'` drives both branches through the public call path.
  - evidence: `core/deprecated.go` (`deprecatedRoute`) wraps both call paths (`core/native_effects.go`, `core/native_flow.go`); `core/deprecated_test.go` (`lifecycle.deprecated-never-silent`, both branches).
- [x] 2.4 Add the `CHANGELOG.md` Breaking entry and an ADR for the new `Build` failure mode. Verify: the entry names the notice window and the warn-only escape; the ADR file exists under `docs/design/`.
  - evidence: `CHANGELOG.md` `[Unreleased]` → `### Breaking`; `docs/adr/0158-sunset-inside-the-notice-window-fails-build.md`. The task named `docs/design/`; the repository keeps decision records under `docs/adr/`, so the ADR went there.

## 3. Successor option validation

- [x] 3.1 Extend the static `Build` validation pass so each flow's `ModelOptions` is checked against the declared successor's `Caps`, failing with the option name when the successor lacks it (e.g. `Caps.Temperature`, `core/types/model_profile.go:55`). Verify: `go test -short -timeout 2m ./core/... -run 'TestSuccessorParamsValidated'` fails naming the option.
  - evidence: `core/deprecated.go` (`checkSuccessorOptions`: `temperature`→`Caps.Temperature`, `tool_choice`→`Caps.Tools`, `response_format`→`Caps.Constrained`), called from `core/native_resolve.go`; `core/deprecated_test.go` (`lifecycle.successor-params-validated`).

## 4. Cost tags forwarded

The provider-metadata contract now names the field (`ModelRequest.Metadata`, `RequestMetadata{CostTags}`), so the request-side forwarding is unblocked and landed below. The adapter-side mapping onto a real provider's metadata channel stays with the provider-adapter work.

- [x] 4.1 Forward a run's `CostTags` (`core/types/identity.go:80`) onto the provider request accounting path for a provider that accepts request metadata, and record `gohan.cost.per_run{flow}`. Depends on the `model` provider-metadata contract naming the request-metadata field. Verify: `go test -short -timeout 2m ./core/... -run 'TestCostTagsForwarded'` asserts the tags reach the request seam and the metric carries the run total.
  - evidence: `m1-provider-metadata-contract` names the field `ModelRequest.Metadata` (`RequestMetadata{CostTags}`); `core/types/model_request.go` carries it in the frozen wire order (zero value stays off the wire, cassette replay keys unchanged), `std/assembly.go` populates it from `in.Run.CostTags`, and `core/chains/limits.go` records `gohan.cost.per_run{flow}` at the charge site with `RecordCost` dedupe. `core/cost_tags_test.go` (`lifecycle.cost-tags-forwarded`) drives `Build` + `Send` and asserts both halves. The adapter-side mapping onto a real provider's metadata channel is still the adapters' duty.
- [x] 4.2 Record the adapter-side forwarding as a blocker owned by the provider-adapter work: `adapter/` holds only `otel`, so no adapter forwards the tags yet. Verify: blocker recorded in `design.md` and this task; unblock when a provider adapter lands.
  - evidence: recorded here and in `design.md` D4/Non-Goals; `adapter/` contains only `otel`.

## 5. Cache-write socialisation

Blocked on `m1-exact-match-cache`, which is not authored yet; the seam the fold needs does not exist.

- [ ] 5.1 Fold the cache-write premium of a run that repopulated the prefix cache into `Pricing.CachedInput` (`core/types/model_profile.go:81`) when `Pricing.SocializeCacheWrites` (`:85`) is on, so cached reads carry the overhead rate and the repopulating run is not charged. Verify: `go test -short -timeout 2m ./core/... -run 'TestCacheWriteSocialized'` once the cache seam exists.

## 6. Repository gates

- [x] 6.1 Run the repository gates after implementation lands. Verify: `task lint && task test && task spec && task api:check`; `task spec` reports `missing_in_scope=0`.
  - evidence: `task lint` 0 issues; `task test` green; `task spec` `missing_in_scope=0`, `unregistered=0`; `task api:check` reports the `otel` module compatible.
