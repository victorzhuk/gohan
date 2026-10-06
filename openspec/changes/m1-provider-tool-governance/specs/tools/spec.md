# Spec Delta

## MODIFIED Requirements

### Requirement: Provider-executed tools

The implementation MUST carry the *Provider-executed tools* contract on the public path. A provider-executed tool MUST be registered through the ordinary tool registry as a spec with `Executor: ByProvider`, and its effect, trust and `Capabilities.Exfil` MUST be read from that registered spec and never from the provider's block. The harness MUST NOT invoke the tool's `Call` for a `ByProvider` spec; the provider runs it inside the model request. `Build` MUST already have refused a `ByProvider` spec whose name is absent from the profile's `Caps.ProviderTools` with `ErrProviderToolUnsupported` (`core/build.go:244-251`). The permission gate MUST run on the spec before the model request is assembled, so the gate decision is a declaration decision: a `Deny` MUST leave the tool out of the request, an `Ask` MUST use the provider approval flag when `Caps.ProviderTools` marks approval supported and MUST otherwise behave as `Deny`, and an `Allow` MUST declare the tool. An approval request from the provider MUST become a suspension carrying the pending call and the matched argument origins, not an inline wait. A provider tool result MUST carry the origin of the tool that produced it, `OriginProvider{Name}`, on every block it appends.

Tools the provider runs inside the model call (web search, code execution, hosted MCP) are registered like any other tool with `Executor: ByProvider`; `std/tool/provider` ships `WebSearch(opts...)`, `CodeExec()` and `HostedMCP(url, allowed ...string)`, all `Untrusted`, with effect `ReadOnly` for search and `SideEffect` for the other two, and `Exfil: true` for search and hosted MCP. They ship their own `ToolPolicy` raising `MaxEffect` to the declared effect, so the import cap does not apply to them; a wiring that lowers `MaxEffect` below the declared effect still caps it and increments `gohan.tool.effect_capped` (`tools.untrusted-effect-cap`). Rules:

1. **Enable path.** The adapter translates the spec into the provider's tool declaration (search `max_uses` from the run's remaining `MaxToolCalls`, domain allow/deny from `WithDomains`, hosted-MCP `allowed_tools` from `allowed`). A profile whose `Caps.ProviderTools` does not list the tool's kind fails `Build` with `gohan.tool.provider_unsupported`.
2. **Gate before the call.** Because the call happens inside the model request, the permission gate runs on the spec before the request is sent: `Deny` → the tool is not declared; `Ask` → declared with the provider's approval flag (`require_approval: always`) when `Caps.ProviderTools` marks approval as supported, otherwise treated as `Deny`; `Allow` → declared. A provider approval request maps to `HumanApproval` with an `ApprovalRequest` built from the pending call; `Resume(Approve())` replays the model call carrying the approval item under the same `CallKey`.
3. **Post-hoc taint.** The model's inputs to a provider tool are only visible after the fact. `std/taint` runs rule 1 of `taint` on them when the response arrives; a tainted input into an `Exfil` provider tool aborts the run `Failed(Permanent)` before any result block is appended or used, and `gohan.taint.post_hoc{tool}` increments.
4. **Provenance.** Adapters convert the provider's call block to a `ToolUse`; the executor is the `Executor: ByProvider` of the registered spec with that name, not a block field, and its result block to a `ToolResult` whose blocks carry `OriginProvider{Name}`; opaque payloads the provider requires back (encrypted search content, citations) stay inside the result as `Raw` and round-trip per `model` rule 8. Fencing, guards and taint apply unchanged.
5. **Cost, limits, journal.** Each provider call counts toward `MaxToolCalls`, is journaled `Completed` directly (never `Reserved`; it cannot be replayed or verified), is audited with `Executor`, and is priced from `Pricing.ProviderCall[name]` using `Usage.ProviderToolCalls`.
6. **Trifecta.** A provider tool with `Exfil` is both the exfiltration leg and the untrusted-content leg of the `taint` build check.

#### Scenario: provider tool registered
ID: `tools.provider-tool-registered`
- WHEN a flow registers `provider.WebSearch()` and the profile lists web search in `Caps.ProviderTools`
- THEN the model request declares the provider's search tool and `Explain` lists it with `Executor: ByProvider`

#### Scenario: gate decides before the call
ID: `tools.provider-tool-gate-before-call`
- WHEN the tool policy denies `web_search` for the principal
- THEN the model request carries no search tool and no provider search occurs

#### Scenario: provider approval as suspension
ID: `tools.provider-approval-as-suspension`
- WHEN a hosted-MCP tool is gated `Ask` and the provider returns an approval request
- THEN the run suspends with `HumanApproval` naming the pending call, and `Resume(Approve())` replays the model call with the approval under the original `CallKey`

#### Scenario: provider result carries origin
ID: `tools.provider-result-origin`
- WHEN a provider search returns result blocks
- THEN they are appended as a `ToolResult` with `OriginProvider{"web_search"}` and the encrypted payload round-trips as `Raw` on the next call
