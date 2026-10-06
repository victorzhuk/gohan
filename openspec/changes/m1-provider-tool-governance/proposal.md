# Proposal: m1-provider-tool-governance

## Why

The `tools`, `taint` and `model` specs already describe provider-executed tools (web search, code execution, hosted MCP): `openspec/specs/tools/spec.md:332` *Provider-executed tools*, `openspec/specs/taint/spec.md:54` rule 7, and `openspec/specs/model/spec.md:276` rule 8. Nothing implements them. `openspec/changes/m1/design.md:61` records all four `tools.provider-*` IDs as `missing` with "no provider-executed tool path exists", `:98` records `model.provider-tool-usage` as `missing` with "`Usage.ProviderToolCalls` ships as a field; nothing increments it", and `:50` records `taint.provider-tool-post-hoc` as `missing`. The two provider adapters authored next (`m1-openai-provider`, `m1-anthropic-provider`) cannot emit a governed provider tool until this path exists. This change adds that path.

## What Changes

- Classify a provider-executed tool by `Executor: ByProvider` on the registered spec, register it through the ordinary tool registry, and validate the enable path at `Build` against `Caps.ProviderTools`.
- Run the permission gate on the spec before the model request is assembled, and translate the verdict into the provider declaration: `Deny` omits the tool, `Ask` uses the provider approval flag where the profile supports it and otherwise behaves as `Deny`, `Allow` declares it.
- Model a provider approval request as a `HumanApproval` suspension built from the pending call, resumed by replaying the model call under the original `CallKey`.
- Convert the provider's call block to a `ToolUse` and its result to a `ToolResult` whose blocks carry `OriginProvider{Name}`, keeping opaque round-trip payloads as `Raw`.
- Check taint post hoc on the inputs the provider reported: a tainted input into an `Exfil` provider tool fails the run `Permanent` before any result block is appended.
- Count and price provider tool calls in `Usage.ProviderToolCalls`, charged through the existing `Pricing.ProviderCall` path.
- Record the dependency on `m1-provider-conformance-contract` (shared suite) and on both adapter changes. Adapter internals are out of scope.

Non-breaking: this adds a path that has no implementation today; no shipped public behavior is removed.

## Capabilities

### New Capabilities
- (none)

### Modified Capabilities
- `tools`: modify *Provider-executed tools* to bind registration and classification, gate-before-call, approval-as-suspension, and result-origin to the implementing path.
- `taint`: modify *Enforcement* to bind the post-hoc provider-tool check and its ordering and metric.
- `model`: modify *Provider adapter contract* to bind provider tool usage accounting and the non-`Raw` conversion of provider tool blocks.

## Impact

Affected: `core` (gate invocation for a declared tool, approval suspension, request assembly, provider block conversion), `std/tool/provider` (new package: `WebSearch`, `CodeExec`, `HostedMCP`), `core/build.go` (enable-path validation, already partly present at `core/build.go:244-251`), `core/chains/limits.go` (provider-call pricing, already present at `core/chains/limits.go:142-144`). Depends on `m1-taint-enforcement` (shipped) for the taint hook and `OriginProvider` (`core/types/message.go:34`). Depends on `m1-provider-conformance-contract` and the two adapter changes for the adapter-side conversion and usage fill; this change does not specify adapter internals. No release is cut and no spec outside this package is edited.
