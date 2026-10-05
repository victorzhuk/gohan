# 0156. Network reachability is declared on ToolSpec, and the audit trail records egress denials

Status: accepted

## Context

ADR-0155 fixed which package owns `Capabilities.Exfil` derivation, but two facts the egress change needs have no home in the shipped declarations.

First, `RequiresEgress` and `CheckEgress` take a `network bool` because, as their own comment says, the flag cannot be read from a `ToolSpec` (`core/types/tool_egress.go:11-17`). Nothing tells `Build` which registrations reach the network: `core/tool.go` ships `WithEffect`, `WithScopes`, `WithTimeout`, `WithRisk`, `Deferred`, `WithRawArgs` and `ExcludeFields`, and no option declares network reach. An implementer asked to wire the refusal would have to invent the flag's source, and a design pass for the M1 egress change left exactly that choice open.

Second, the `tools` capability's `tools.egress-audited` scenario requires an audit record that names the tool, the run id, the host and the reason. `stores.AuditRecord` carries `Kind`, `RunID`, `Tool`, `Decision` and fourteen further fields, and no host or reason (`core/stores/audit.go:16-60`), so the record cannot hold what the scenario asserts, and no `AuditKind` names an egress denial.

Network reach and exfil capability are also not the same fact, and conflating them is a security bug in either direction: the `build` capability requires `Capabilities.Exfil` to be false for a policy whose `Allow` entries are all private, and true once a public host is allowed (`openspec/specs/build/spec.md:66-69`), while both of those tools still reach the network and still need a policy.

## Decision

1. **`ToolSpec` gains `Network bool`.** The field states that the tool reaches the network. It is set at construction by `WithExfil()`, by the `WithHTTP()` option the `tools` Egress requirement already names, and by every gohan-shipped HTTP tool; `Build` passes it as the `network` argument `RequiresEgress` and `CheckEgress` take. It lives on the spec so `Explain` and the pinned manifest can show it.

2. **`WithHTTP()` ships in the change that lands egress.** The `tools` Egress requirement names it as one of the declarations that makes a tool network-capable (`openspec/specs/tools/spec.md:157`). Its live seam is the shipped HTTP tool `std/tool/http.Fetch` and any user tool that performs HTTP itself. A tool that performs HTTP and declares neither `Network` nor a policy fails `Build` with `ErrEgressPolicyRequired`.

3. **Network reach does not imply `Capabilities.Exfil`.** `DeriveExfil` keeps its shipped meaning: with a policy present the value follows the policy; without one the author's declaration stands. A network tool with a private-only `Allow` list is `Exfil: false` and still requires a policy.

4. **`stores.AuditRecord` gains `Host string` and `Reason string`, and `AuditEgressDenied AuditKind = "egress_denied"` joins the kind list.** The refusal path appends a record carrying the tool, the run id, the resolved host and the reason. The record and the `gohan.egress.requests{tool, host, decision}` metric carry no URL, path, query, user-info, `Authorization` value, or body: those are the fields that can carry a secret, and `tools.egress-audited` requires that the record names the host and reason and never the request body.

## Consequences

- The `tools` and `stores` capability Contracts are edited with this ADR: `ToolSpec` gains `Network`, `AuditRecord` gains `Host` and `Reason`, and the audit kind list gains `egress_denied`. `docs/design/types.md` is regenerated in the same commit.
- Adding fields to `AuditRecord` changes no store port method; the memory and later Postgres implementations persist the new fields, and a record written before the change keeps its meaning because the zero values are empty.
- `Explain` and the manifest can now answer which registered tools reach the network without a policy, which is what an operator reviewing a trifecta needs.
- A tool that dials without declaring `Network` is outside gohan's control. The tool author contract states that a user tool reaches the network through `EgressClient(ctx)`; the declaration is what `Build` can refuse, and the design of the egress change states this boundary rather than implying a sandbox that does not exist.

## Alternatives

- Inferring reach from `Executor == ByProvider`. Rejected: provider-executed tools run at the provider and are not model-directed egress; the M1 provider change gives them their own governance.
- Inferring reach from `spec.Egress != nil`. Rejected: circular — the policy is the thing whose absence must fail the build.
- An optional interface on the tool value (`ReachesNetwork() bool`) asserted by `Build`. Rejected: it needs a type assertion at the build site, it cannot appear in `Explain` or the pinned manifest, and `ToolSpec` already carries the sibling facts (`Capabilities`, `Effect`, `Trust`).
- A free-form `Meta map[string]string` on `AuditRecord` instead of two fields. Rejected: the audit trail is a fixed record shape that later queries and the Postgres implementation read; a map would move the schema into every writer.
