# Proposal: m1-model-retirement-cost

## Why

The `lifecycle` spec already fixes model retirement, deprecation and cost
semantics, but no code implements them. The milestone record marks all five
authoring IDs `missing` or `integration gap` and states "no retirement logic
exists" (`openspec/changes/m1/design.md:164-168`). Providers can retire a
pinned model with no build-time warning, and a run that repopulates a prefix
cache is still charged the write premium that a peer flow should share.

## What Changes

- Add the sunset notice window to public `Build`: a pinned profile whose
  `Sunset` falls inside the notice window fails the build, or warns when the
  build opts out, naming the profile and its successor.
- Make a provider deprecation never silent: a call classified `ClassDeprecated`
  routes to `Successor` when declared and otherwise surfaces as a
  `*ModelError{Class: Deprecated}`, with no fallback assistant message.
- Validate a flow's model options against the successor profile at `Build`, so
  a migration is caught before the retired model is reached.
- Forward a run's `CostTags` to the provider accounting path, not only to
  local spans.
- Socialise cache-write cost: the run that repopulates a prefix cache is not
  charged the write premium; cached reads carry the overhead rate.

**BREAKING**: `Build` gains a failure mode for a profile inside the notice
window. A configuration that pins an already-near-retirement profile fails
until the build sets the warn-only option or moves the pin.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `lifecycle`: the "Retirement, schema evolution, cost" requirement gains
  normative text for the notice window, the never-silent deprecation path,
  successor option validation, provider-bound cost tags, and cache-write
  socialisation. Its registered scenarios keep their IDs and WHEN/THEN lines.

The provider metadata the behaviours read (`ModelProfile.Sunset`,
`ModelProfile.Successor`, `Caps`, `Pricing`) lives in `model`
(`core/types/model_profile.go`). That capability's provider-metadata contract
is a separate dependency; the scenario IDs assigned to this change are
`lifecycle`-bound, so this package writes no `model` delta.

## Impact

Affected: the public `Build` option surface (`core/build_options.go`), the
model profile type (`core/types/model_profile.go`), the run identity type
(`core/types/identity.go`), the run-tag assembly path (`std/assembly.go`), and
the provider request assembly that a later adapter owns. No implementation
exists for the notice window: no `NoticeWindow` or `SunsetWarnOnly` symbol
ships anywhere in the tree. `ClassifyProviderError` already maps
`model_not_found`/`model_deprecated`/`model_retired` to `ClassDeprecated`
(`core/types/model_profile.go:123,142`); no non-test caller consumes that
class — the only readers are tests and the conformance fixtures.
`CostTags` ships on `RunInfo` (`core/types/identity.go:42,80`) and reaches
local telemetry (`std/assembly.go:92-94`), but no adapter forwards it to a
provider. `Pricing.SocializeCacheWrites` ships (`core/types/model_profile.go:85`)
and is read by the chain limiter (`core/chains/limits.go:136`), but the
socialisation obligation this change names depends on `m1-exact-match-cache`,
which is not authored yet; this change describes only the socialisation duty
and leaves the cache behaviour to that change.
