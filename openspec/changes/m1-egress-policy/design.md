# Design: m1-egress-policy

## Context

`EgressPolicy`, `EgressDenied`, `Capabilities`, `RequiresEgress`, `CheckEgress`, and `DeriveExfil` already exist. Build must supply the registration fact because `ToolSpec` does not encode network access (core/types/tool.go:44-72; core/types/tool_egress.go:11-24). The capability currently documents the required policy defaults, client behavior, and five M1 scenarios (openspec/specs/tools/spec.md:155-189). Core contains driver contracts, while policy values and defaults belong in `std` (docs/design/architecture.md:101-121). The shared security decisions D1-D7 are sealed in the M1 design and ADR-0155 (openspec/changes/m1/design.md:209-243; docs/adr/0155-m1-taint-and-egress-contract.md:17-34).

## Goals / Non-Goals

**Goals:** Wire tool registration through Build, provide a policy-enforcing client to tools, and implement the assigned `tools` behavior.

**Non-Goals:** This change does not implement sandbox egress, HTTP server behavior, provider transport policy, or release tags. Sandbox tools with non-empty `Egress` remain outside this change (openspec/specs/tools/spec.md:157-159). Provider transports are configuration rather than model input (openspec/specs/tools/spec.md:159).

## Decisions

### D1 — Keep declarations in core and policy execution in std

Add these exported options to `core/tool.go`, using the existing `gohan.ToolOption` type (core/tool.go:25-27):

- `func WithEgress(p types.EgressPolicy) ToolOption` copies `p` into `toolConfig.spec.Egress` and deep-copies its `Allow` and `Schemes` slices. The exact shipped policy type is `types.EgressPolicy` (core/types/tool.go:58-66).
- `func WithExfil() ToolOption` sets the construction-time `Capabilities.Exfil` declaration true. Build later replaces it with `types.DeriveExfil` output when a policy exists, as D5 requires (docs/adr/0155-m1-taint-and-egress-contract.md:27).
- `func WithPrivateRead() ToolOption` sets `Capabilities.PrivateRead` true. This declaration stays separate from egress derivation (core/types/tool.go:44-48).

These options declare facts and policy references; they do not define policy defaults or matching. `std` owns those values under the core budget rule (docs/design/architecture.md:101-121). Do not add a second builder.

### D2 — Network reachability is a declared spec field

`ToolSpec` gains one field, `Network bool` — the registration fact `core/types/tool_egress.go:11-17` requires Build to supply (ADR-0156 section 1). `WithHTTP()` and `WithExfil()` set it, every shipped HTTP tool sets it, and it lives on the spec so `Explain` and the pinned manifest can show which tools reach the network. `WithHTTP()` ships in this change because the Egress requirement names it (`openspec/specs/tools/spec.md:157`); its live seams are `std/tool/http.Fetch` and any user tool that performs HTTP itself (ADR-0156 section 2).

`WithHTTP()` marks reach at every effect tier, including `ReadOnly`: a read-only tool that performs HTTP still reaches the network, so the requirement's enumeration (``WithExfil()``, `Effect` above `ReadOnly` with `WithHTTP()`, or a `std/tool/http` tool) is read fail-closed — the declaration is what `Build` refuses on, and every declared network tool needs a policy (ADR-0156 section 1).

Network reach and Exfil classification stay separate facts and are never inferred from each other: a network tool whose `Allow` entries are all private derives `Exfil: false` and still requires a policy (`openspec/specs/build/spec.md:66-69`; `openspec/specs/tools/spec.md:157`). Build reads the reach flag from the resolved spec, not from the executor and not from the presence of a policy.

For each resolved registered tool, Build calls `types.RequiresEgress(spec, spec.Network)`, then `types.CheckEgress(spec, spec.Network)`, at the point `resolveNative` already materializes the definition's specs (`core/native_resolve.go:41-77`), returning the existing `ErrEgressPolicyRequired`-wrapped error before any provider or tool call. Then Build sets `spec.Capabilities.Exfil = types.DeriveExfil(spec)` and stores that spec in resolved flow tools. This is D5; do not create another classifier (docs/adr/0155-m1-taint-and-egress-contract.md:25-27; core/types/tool_egress.go:15-39). Keep `types.ErrEgressPolicyRequired` as the error value (core/types/errors.go:52-55).

### D3 — Client behavior and constructor

Create `std/egress/client.go` in package `egress` with exact exported API:

```go
func Client(p types.EgressPolicy, opts ...Option) *http.Client
func WithProxy(proxyURL *url.URL) Option
```

`Option` is `func(*config)`, private to the package. `Client` returns an `http.Client` whose transport resolves the destination, checks every resolved address, and dials the selected checked address while retaining the URL hostname for HTTP Host and TLS server-name validation. Redirect handling validates every next URL before following it, rejects disallowed schemes and private addresses, and removes `Authorization` if the redirect host differs. The client enforces `MaxRedirects`, `MaxBytes`, and `Timeout`; it closes the response body after a size-limit refusal. `WithProxy(proxyURL)` accepts an `http` or `https` proxy URL and any other scheme fails `Client`'s construction with `ErrEgressProxyUnsupported`. In proxy mode gohan dials exactly one address — the proxy's, resolved and checked against the policy like any other dial — and every request hop's URL is validated against `Allow`, `Schemes` and the private address literals before the request is sent. Address-level rebinding protection is claimed only for a direct connection: a proxy resolves the target itself, so the design states that limitation in the option's doc comment instead of implying a guarantee it cannot make, and an operator who needs address pinning runs direct. A hop that the policy refuses is refused before it leaves the process.

The test transport must use an internal-package seam around the resolver and dialer. Do not export test-only types. Use the standard-library `net.Resolver` and `net.Dialer` directly in production; the implementation tests replace the internal resolver/dial function without changing public API.

### D4 — Tool call-time access and HTTP tool

Add to `core/tool.go`:

```go
func EgressClient(ctx context.Context) *http.Client
```

The function returns nil when the current tool call has no egress client. The driver installs the resolved client in a private context key around the function call. `NewTool` already passes its call context to `fn` (core/tool.go:107-120,130-152). Do not capture a client on the shared tool value. The resolved `ToolSpec.Egress` and call context select the client for each invocation, preserving concurrent use of a shared `Tool` (core/types/tool.go:97-102).

The enforcement boundary is the declaration, and the design states it rather than implying a sandbox that does not exist: `Build` refuses every registered tool that declares `Network` without a policy, `WithHTTP()` and `WithExfil()` are the declarations a tool author uses, and the tool author contract in `openspec/specs/tools/spec.md` already says a user tool reaches the network through `EgressClient(ctx)`. A tool that dials on its own without declaring `Network` is outside what a library can refuse; gohan's guarantee covers every declared network tool, and the audit record and the metric describe the governed calls only.

Create `std/tool/http/fetch.go` with:

```go
type FetchInput struct {
    URL string `json:"url"`
}
func Fetch(policy types.EgressPolicy) (gohan.Tool, error)
```

The returned tool has stable name `web_fetch`, accepts `FetchInput`, calls `egress.Client(policy).Get` through `gohan.EgressClient(ctx)`, and returns a result containing the bounded response body and status. `Fetch` registers the policy through `gohan.WithEgress(policy)` and marks itself network-capable through the internal built-in registration seam. Its constructor uses the existing generic `NewTool` signature (core/tool.go:123-153). No arbitrary tool name or custom description option is needed for this built-in.

### D5 — Audit and metric contract

`core/stores.AuditRecord` currently stores `Kind`, `RunID`, `Tool`, and `Decision`, but has no host or reason fields (core/stores/audit.go:34-60). Extend it with `Host string` and `Reason string`, and add `AuditEgressDenied stores.AuditKind = "egress_denied"` beside the existing audit kinds (core/stores/audit.go:16-32). The egress denial path appends `stores.AuditRecord{Kind: stores.AuditEgressDenied, RunID: ..., Tool: ..., Host: ..., Reason: ...}`. Obtain the `stores.AuditLog` from the same run context binding used by the harness; `WithDecisionAudit` demonstrates this audit-log context pattern (core/session_takeover.go:38-48). If no audit log is bound, follow the existing optional audit behavior and do not fail the tool call.

Increment the metric through `types.TelemetryFrom(ctx).Count(ctx, "gohan.egress.requests", 1, types.String("tool", tool), types.String("host", host), types.String("decision", decision))`. `types.Telemetry.Count`, `types.TelemetryFrom`, and `types.String` are shipped (core/types/telemetry.go:5-18,24-47). Use `decision=denied` for refusals. The audit record and the metric carry the resolved host and the decision only: no URL, path, query string, user-info, `Authorization` value or body ever reaches an audit field or a metric attribute, because those are the values that can carry a secret (ADR-0156 section 4). The `URL` on the model-facing `types.EgressDenied` is the URL the model itself supplied, and it is never written to the audit trail. The audit schema stores checksums and sizes rather than content (core/stores/audit.go:34-60).

A refusal is `types.EgressDenied{URL, Reason}` (core/types/tool.go:68-72), surfaced with `types.Permanent` at the tool-result boundary as required by the contract (openspec/specs/tools/spec.md:159). Build refusal wraps the existing `types.ErrEgressPolicyRequired` and names the tool (core/types/tool_egress.go:19-24; core/types/errors.go:52-55).

### D6 — Preserve shared taint and compatibility contracts

Build-time Exfil derivation is the only classifier, and the taint hook reads the resolved `ToolSpec.Capabilities.Exfil` (docs/adr/0155-m1-taint-and-egress-contract.md:25-29). Build must call `RequiresEgress`, `CheckEgress`, and `DeriveExfil` for every registered tool (docs/adr/0155-m1-taint-and-egress-contract.md:27). This change does not alter the permission precedence or taint window decisions (openspec/changes/m1/design.md:209-243). Sandbox tools with non-empty `Egress` remain outside this change (openspec/specs/tools/spec.md:157).

### D7 — Seed public-path proofs without external network access

Use `httptest` servers and deterministic resolver/dial instrumentation. Register tools through the public `NewTool` and `Build` path, then invoke through a built native flow. Assert observable network calls, model-visible results, audit fields, and metric attributes.

- Exfil declaration without policy → `Build` returns an error matching `types.ErrEgressPolicyRequired`; provider and tool call counters remain zero.
- Metadata URL → resolver identifies link-local address; dial counter remains zero; model-visible result is permanent `EgressDenied` with reason `link-local`; audit and denied metric omit bodies.
- Allowed host redirects to `http://10.0.0.5/` → deny before second request; return permanent `EgressDenied` with reason `private range`; do not expose the first response body.
- Checked hostname resolves to a public test address, then would resolve to loopback → dial records the checked address and the second resolution is not used for dialing.
- Oversized response → the body is cut at `MaxBytes`, the result is `Failed(Permanent)` naming the limit, and the connection is closed (`openspec/specs/tools/spec.md:181-184`).

The assigned M1 registry IDs are `tools.egress-metadata-blocked`, `tools.egress-redirect-revalidated`, `tools.egress-rebinding-safe`, `tools.egress-size-limit`, and `tools.egress-audited` (openspec/changes/m1/design.md:52-60). The `tools.egress-policy-required` scenario is M0-ranked and already has contract-level coverage; this change closes its Build integration gap without assigning it as a new M1 scenario (openspec/changes/m1/design.md:66-70; core/types/tool_egress.go:11-24).

## Risks / Trade-offs

- [Risk] Proxy routing can move DNS resolution outside the checked dialer. → Accepted and bounded by D3: `WithProxy` takes `http`/`https` only, dials and checks the proxy's own address, validates every hop URL, and its doc comment states that address-level rebinding protection applies to direct connections only.
- [Risk] Attaching a client to shared tool state can mix policies across flows. → Inject the resolved client through the call context only (core/types/tool.go:97-102).
- [Risk] Audit schema changes add fields to a core store record. → Keep fields limited to host and reason and record no body content (core/stores/audit.go:34-60).

## Migration Plan

No data migration is required. The main-spec edits land with this change under ADR-0156: `ToolSpec` gains `Network`, `stores.AuditRecord` gains `Host` and `Reason`, `AuditEgressDenied` joins the kind list, `docs/design/types.md` is regenerated, and the `tools` and `stores` Contracts are updated in the same commit (`docs/adr/0156-network-declaration-and-egress-audit.md`). Existing network-capable tools must declare an egress policy before Build; Build then refuses undeclared network reachability using the existing sentinel (core/types/tool_egress.go:19-24). Keep the module pre-v1 and do not create release tags during M1 authoring (openspec/changes/m1/design.md:20-27).
