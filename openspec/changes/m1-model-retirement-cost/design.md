# Design: m1-model-retirement-cost

## Context

See `proposal.md` for motivation. The milestone record assigns the five
`lifecycle` IDs in this package and records the status of each
(`openspec/changes/m1/design.md:164-168`). The design below decides only what
the `lifecycle` requirement text leaves open: where the notice-window check
runs, which symbol carries each behaviour, and how the two dependency
boundaries are held.

State that exists today, read this session:

- `ModelProfile.Sunset time.Time` and `ModelProfile.Successor string` ship
  (`core/types/model_profile.go:13-14`). No code reads either for admission.
- `ClassifyProviderError` maps the code list
  `{model_not_found, model_deprecated, model_retired}`
  (`core/types/model_profile.go:123`) to `ClassDeprecated`
  (`core/types/model_profile.go:142`). No non-test caller consumes
  `ClassDeprecated`; the only readers are tests and the conformance fixtures
  (`core/types/model_profile_test.go:103`, `testkit/gohantest/scripted_model_test.go:158`,
  `testkit/conformance/fixtures.go:111`, `testkit/conformance/model_test.go:93`).
  `core/types/problem.go:232` only classifies it for rendering.
- `Caps.Temperature` ships (`core/types/model_profile.go:55`) with the rest of
  the `Caps` struct. No successor-capability validation exists.
- `CostTags{Feature, Environment, CostCenter}` ships on `RunInfo`
  (`core/types/identity.go:42,80`) and feeds the prefix-cache key builder
  (`std/assembly.go:92-94`, the `feature`/`cluster`/`costCtr` fields of
  `prefixID`); no adapter forwards the tags to a provider accounting path.
- `Pricing.SocializeCacheWrites` ships (`core/types/model_profile.go:85`) and
  is read by the chain limiter (`core/chains/limits.go:136`). The prefix-cache
  behaviour it socialises does not exist yet.
- `Build` already owns static option validation and takes options through
  `With*` functions (`core/build_options.go:48-171`). No `NoticeWindow` or
  `SunsetWarnOnly` symbol exists anywhere in the tree.

## Goals / Non-Goals

**Goals:**

- Turn the five `lifecycle` scenarios into observable public behaviour with a
  named seam each.
- Hold the two dependency boundaries: provider metadata (`model`) and the
  prefix cache (`m1-exact-match-cache`) stay owned elsewhere; this change
  binds only the obligations its scenarios name.

**Non-Goals:**

- No `model` capability delta. The assigned scenario IDs are `lifecycle`-bound
  and a delta under `model` would need its own registered IDs.
- No prefix-cache implementation and no cache-hit accounting. That is
  `m1-exact-match-cache`; this change socialises the cost once that cache
  exists.
- No provider adapter module. `adapter/` holds only `otel`
  (`openspec/changes/m1-provider-conformance-contract/design.md`), so the
  cost-tag forwarding seam is specified, not implemented, until an adapter
  lands.

## Decisions

### D1. The notice-window check runs in `Build` as a new option pair

`Build` gains a notice-window duration defaulting to 30 days and a warn-only
boolean, both in the existing `With*` option style (`core/build_options.go`).
Rationale: the scenario's THEN is "`Build` fails (or warns under
`SunsetWarnOnly`)", so admission is static. A run-time check would surface the
retirement on the day of the call, which the milestone record calls
"a discovery on shutdown day" (`openspec/specs/model/spec.md:261`).

The check reads `ModelProfile.Sunset` and `ModelProfile.Successor`
(`core/types/model_profile.go:13-14`), which already ship. A zero `Sunset`
passes: the field's zero value means no announced retirement. The failure
names the profile and the successor, per the scenario.

Alternatives: a manifest-only CI check. Rejected as the sole seam — the
scenario binds `Build`, and a CI check does not protect a run started from a
stale manifest.

### D2. Deprecation reuses the shipped classifier and routes at the call site

The classifier already exists (`core/types/model_profile.go:132,142`). The
change adds the consumer: on `ClassDeprecated`, the call routes to the
profile's `Successor` when `Successor` is non-empty, and otherwise returns
`*ModelError{Class: Deprecated}` unchanged. No fallback assistant message is
produced, per the scenario. The implementation emits
`gohan.model.deprecated_calls{profile}` for every deprecated call and
`gohan.model.deprecated_routed{profile,successor}` on reroute. The warning path
in D1 emits `gohan.model.sunset_warning{profile}`. These names are new; the
`model` main spec does not name them, so the `lifecycle` requirement prose
declares them and marks them as metrics the implementation must emit.

Rationale: no new error class and no new classification table. The mapping
seam is the one the sibling provider contract already fixes
(`openspec/specs/model/spec.md:269`).

### D3. Successor option validation shares the static `Build` validation path

`Build` validates each flow's `ModelOptions` against its profile's `Caps`
today or will once the conformance contract lands. This change extends that
same pass to also validate against the declared successor's `Caps`, so a flow
that sets temperature against a successor without `Caps.Temperature`
(`core/types/model_profile.go:55`) fails at build. The error names the option,
per the scenario.

Rationale: reusing the existing static validation keeps one admission pass and
one error shape; a second, parallel validator would be a second convention.

### D4. Cost tags are forwarded on the provider request assembly seam

The local half ships: `RunInfo.CostTags` (`core/types/identity.go:80`) is read
into telemetry at `std/assembly.go:92-94`. The change adds the missing half:
the provider request assembly attaches the run's `CostTags` as provider
request metadata for a provider that accepts it, and the accounting path
records `gohan.cost.per_run{flow}` for the run total. The request-metadata
field on the profile is part of the `model` provider-metadata contract, a
separate dependency; this requirement binds the forwarding and the metric, not
the field.

Rationale: the scenario's THEN is "spans carry them and providers that accept
metadata receive them"; the first clause already holds, the second is the
addition.

### D5. Cache-write socialisation is a cost rule over the `m1-exact-match-cache` seam

`Pricing.SocializeCacheWrites` (`core/types/model_profile.go:85`) ships. When
it is on, the run that repopulates the prefix cache is not charged the
cache-write premium and the cost folds into `Pricing.CachedInput`
(`core/types/model_profile.go:81`), which cached reads carry as an overhead
rate. The prefix-cache behaviour itself is owned by `m1-exact-match-cache`,
which is not authored yet; this change states the socialisation rule and
depends on that change for the cache to exist.

Rationale: splitting the obligations keeps the cache work out of a
retirement-and-cost change and keeps this change's scenarios provable once
both land. The dependency is recorded in the milestone matrix
(`openspec/changes/m1/design.md:168`).

## Risks / Trade-offs

- **A near-retirement pin now fails the build** → an unreleasable configuration
  becomes visible. Mitigation: the warn-only option (D1) and a `CHANGELOG.md`
  Breaking entry; the change carries an ADR, as every M1 breaking change that
  touches a normative spec does (`openspec/changes/m1/design.md` §1).
- **New metric names could drift from the `model` capability's naming** →
  names follow `gohan.<area>.<event>` (`gohan.model.deprecated_calls`,
  `gohan.model.deprecated_routed`, `gohan.model.sunset_warning`). Mitigation:
  the requirement prose is the authority until the `model` metadata change
  names them.
- **Cost-tag forwarding has no adapter to land in** → the obligation is
  specified and left as an implementation prerequisite. Mitigation: the
  requirement names the provider-metadata dependency; the tasks record the
  blocker rather than assigning an unresolved API to a coder.
- **Socialisation could be read as owning the cache** → the requirement prose
  and D5 mark the cache behaviour as `m1-exact-match-cache`'s.

## Migration Plan

1. Land the notice-window check in `Build` with the default window and the
   warn-only option.
2. Land deprecation routing on the shipped classifier and emit the three
   metrics.
3. Extend the static `Build` validation pass to the successor's `Caps`.
4. Land cost-tag forwarding when a provider adapter exists; record
   `gohan.cost.per_run{flow}`.
5. Land socialisation against the `m1-exact-match-cache` seam once that change
   is authored.

Rollback: each step is a separate build option or call-site branch; reverting
the build option restores the previous admission behaviour.

## Open Questions

None. The dependency boundaries (provider metadata, prefix cache) are named as
dependencies rather than left open, and every decision above is derivable from
the shipped field shapes and the scenario THENs.
