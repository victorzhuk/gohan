# ADR-0112: Provider-executed tools are governed tools with `Executor: ByProvider`

Status: accepted · Origin: grill round 38 (2026-09-30)

## Decision

Web search, code execution and hosted MCP that run inside the provider's model call are registered as ordinary `ToolSpec`s with `Executor: ByProvider` (`std/tool/provider`), `Untrusted`, and `Exfil` where applicable. The gate runs on the spec before the request (deny → not declared; ask → provider approval flag or deny); a provider approval request is a `HumanApproval` suspension replayed under the same `CallKey`. Results are converted to `ToolResult` with `OriginProvider{Name}` (opaque payloads kept as `Raw` inside), so fencing and taint apply; inputs are taint-checked post hoc and an `Exfil` violation aborts the run before results are used. Calls count toward `MaxToolCalls`, are journaled `Completed`, audited with `Executor`, and priced from `Pricing.ProviderCall` via `Usage.ProviderToolCalls`. `Build` fails when the profile's `Caps.ProviderTools` lacks the kind.

## Context and evidence

Provider web search executes with no client round trip and returns its own block types and a separate usage counter; hosted MCP on OpenAI returns list/call/approval items with a per-tool `require_approval`, and the provider itself warns that a malicious server can exfiltrate anything in context. gohan's only prior answer was the `Raw` escape hatch, which left these calls outside the gate, taint, journal, audit, limits and cost.

## Consequences

`tools` v1.1 (`Executor`, provider-tools requirement, four scenarios), `model` v1.1 (`Usage.ProviderToolCalls`, `Caps.ProviderTools`, `Pricing.ProviderCall`, one scenario), `taint` v1.2 (rule 7, one scenario), `limits` v1.2 and `build` v1.1 (one scenario each); tasks 6 and 21; adapter mapping scenarios deferred to M1.
