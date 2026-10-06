# Spec Delta

## MODIFIED Requirements

### Requirement: Egress

The implementation MUST apply the following Egress contract. `EgressPolicy` is the one vocabulary for where a tool may connect: `Allow` lists host globs with optional ports; `Schemes` defaults to `https`; `PrivateRanges` defaults to `DenyPrivate` (RFC 1918, loopback, link-local including cloud metadata, IPv6 ULA, CGNAT); `MaxRedirects` defaults to 3 and every hop is re-validated; `MaxBytes` defaults to 8 MiB; `Timeout` defaults to `ToolSpec.Timeout`. A tool declares it with `WithEgress(p)`; `Build` fails with `ErrEgressPolicyRequired` for a tool that declares network capability (`WithExfil()`, `Effect` above `ReadOnly` with `WithHTTP()`, or a `std/tool/http` tool) without one, and derives `Capabilities.Exfil` from the policy: any `Allow` entry outside the private ranges makes the tool exfil-capable. `sandbox.SandboxPolicy.Egress` is the same type.

`std/egress.Client(p, opts...) *http.Client` enforces the policy: it resolves the host, checks every resolved address against the policy and dials the checked address with the original `Host` (rebinding-safe), repeats the check on each redirect hop, strips `Authorization` on a cross-host redirect, enforces scheme, size and time, and routes through `WithProxy(url)` when set while still validating hops. A refusal returns `EgressDenied{URL, Reason}` (`Permanent`) to the model, appends an audit record `egress_denied` with the run id, and increments `gohan.egress.requests{tool, host, decision}`. Every gohan-shipped HTTP tool (`std/tool/http.Fetch`, the blob URL fetch in `messages`) uses it; user tools receive it from `NewTool` through `EgressClient(ctx)`. MCP client transports and provider adapters are configuration, not model input, and are exempt; `Explain` lists their hosts beside the tool policies. The Build integration derives network registration from the registered tool source and execution configuration, because `ToolSpec` alone does not encode whether a tool reaches the network. Build applies `RequiresEgress`, `CheckEgress`, and `DeriveExfil` to every registered tool and stores the derived capability in its resolved spec.

#### Scenario: egress policy required
ID: `tools.egress-policy-required`
- WHEN a tool is registered with `WithExfil()` and no `WithEgress`
- THEN `Build` fails with `ErrEgressPolicyRequired` naming the tool

#### Scenario: metadata endpoint blocked
ID: `tools.egress-metadata-blocked`
- WHEN the model calls `web_fetch` with `http://169.254.169.254/latest/meta-data/`
- THEN the tool returns `EgressDenied{Reason: "link-local"}`, no connection is opened and `gohan.egress.requests{decision=denied}` increments

#### Scenario: redirect re-validated
ID: `tools.egress-redirect-revalidated`
- WHEN an allowed host answers with a 302 to `http://10.0.0.5/`
- THEN the redirect is not followed, the result is `EgressDenied{Reason: "private range"}` and the first response body is not returned

#### Scenario: rebinding safe
ID: `tools.egress-rebinding-safe`
- WHEN an allowed hostname resolves to a public address at check time and to `127.0.0.1` on a second resolution
- THEN the connection is made to the checked public address and no request reaches loopback

#### Scenario: size limit
ID: `tools.egress-size-limit`
- WHEN a response exceeds `MaxBytes`
- THEN the body is cut at the limit, the result is `Failed(Permanent)` naming the limit, and the connection is closed

#### Scenario: egress audited
ID: `tools.egress-audited`
- WHEN a request is denied
- THEN the audit record names the tool, the run id, the host and the reason, and never the request body
