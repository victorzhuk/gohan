# ADR-0129: One egress policy and one enforcing client for every network-reaching tool

Status: accepted · Origin: grill round 55 (2026-09-30) · Amends ADR-0093, ADR-0119

## Decision

`EgressPolicy{Allow, Schemes, PrivateRanges, MaxRedirects, MaxBytes, Timeout}` lives on `ToolSpec.Egress` and is the same type as `SandboxPolicy.Egress`. `Build` requires it for any tool that reaches the network and derives `Capabilities.Exfil` from it. `std/egress.Client` enforces it: resolve-then-connect against private ranges (metadata and loopback included), re-validation of every redirect hop, credential stripping on cross-host redirects, scheme, size and time limits, optional org proxy, audit on denial and a per-request metric. gohan's own HTTP tools and the blob URL fetch use it; user tools get it via `EgressClient(ctx)`. Provider and MCP transports are configuration and exempt.

## Context and evidence

Agent-tool SSRF needs no input field: an attacker steers the model into returning a URL the tool fetches, reaching cloud metadata credentials and internal services; one-time hostname checks are bypassed by redirects and DNS rebinding, as recent agent-framework CVEs show. gohan had sandbox egress and taint on URL strings but no policy or client for harness-side HTTP tools, and ADR-0119 referred to a policy that did not exist.

## Consequences

`tools` v1.4 (`EgressPolicy`, `EgressDenied`, `ErrEgressPolicyRequired`, *Egress* requirement, six scenarios), `sandbox` v1.2 (typed egress, one scenario), `build` v1.3 (one scenario), `messages` wording and catalog rows, layout `std/egress`, `std/tool/http`; task 6; client and tools M1, sandbox scenario M2.
