# Design: m1-provider-tool-governance

## Context

See `proposal.md` — Why. The behaviour is already normative in the capability specs; this design decides the seams that connect it to the live tree. Read against the tree on 2026-10-07:

- **Registered, but never run.** `Executor: ByProvider` exists (`core/types/tool.go:41`). `Build` already refuses a `ByProvider` spec whose name is absent from the profile's `Caps.ProviderTools` with `ErrProviderToolUnsupported` (`core/build.go:21-23`, `core/build.go:244-251`). `Caps.ProviderTools map[string]ProviderToolCap` and `ProviderToolCap{Approval}` exist (`core/types/model_profile.go:62`, `:92`). Nothing else about the path exists: `std/tool` holds `exec` and `http` only (`otel` lives in `adapter/otel`), so `WebSearch`, `CodeExec` and `HostedMCP` are absent.
- **The gate is execution-time.** The permission gate is a `ToolMiddleware` (`core/permission/gate.go:113`) that runs when a tool call executes, and its order is scopes → live deny → taint hook → session grant and decider (`core/permission/gate.go:166-195`), matching sealed D6 (`openspec/changes/m1/design.md:239`). A provider tool never executes in the harness, so no invocation site reaches the gate for it. Approval machinery exists — `AskError` with `Invocation`, `ApprovalRequest.ArgOrigins` (`core/permission/approval.go:71`) built by `ArgOrigins` (`core/permission/approval.go:112`) — but no provider path calls it.
- **No provenance conversion.** `OriginProvider` is a constant (`core/types/message.go:27-36`) and `BlockBase.Origin` (`core/types/message.go:49-52`, with `Origin` itself at `:38-41`) carries origin on every block, but nothing constructs `OriginProvider{...}`: no conversion from a provider tool block exists.
- **Usage field ships, fill does not.** `Usage.ProviderToolCalls map[string]int` exists (`core/types/model_types.go:39`); pricing already charges it (`core/chains/limits.go:142-144`); the matrix records "nothing increments it" (`openspec/changes/m1/design.md:98`). `core/chains/limits.go:144` reads `p.ProviderCall[name]`.
- **Taint.** The hook and the post-hoc check are named (`openspec/specs/taint/spec.md:54` rule 7); the taint-enforcement change shipped the hook and gate order. No post-hoc site exists.

## Goals and non-goals

**Goals:** land the declaration decision, the approval suspension, the provenance conversion, the post-hoc taint check and the usage fill so the two provider adapters can emit a governed provider tool. Bind the four `tools`, one `taint` and one `model` scenario IDs to public paths.

**Non-goals:** adapter internals (wire formats, tool-declaration JSON, stream parsing) — those are `m1-openai-provider` and `m1-anthropic-provider`, run against the shared suite owned by `m1-provider-conformance-contract`. This change does not define a second suite, a second builder, or a new model capability. No hedging (`m1-hedged-model-calls`), no cache, no release.

## Decisions

### D1 — Classification is by registered spec, not by block

A provider-executed tool is registered in the ordinary registry as a spec with `Executor: ByProvider`. Effect, `Trust`, `RequiredScopes` and `Capabilities.Exfil` are read from that spec. The provider's call block is matched to the spec by name; the block carries no executor field. The harness never calls `Tool.Call` for a `ByProvider` spec. Rationale: the alternatives — a separate provider-tool registry, or reading classification from the block — each create a second identity for a name and break collision, scope and manifest checks (`tools` *Names and identity*). The `Caps.ProviderTools` enable check stays in `Build`, where it already is.

### D2 — The gate runs before request assembly, on the spec

The pre-call gate for a declared provider tool is a new invocation site, distinct from the execution-time `ToolMiddleware`: `Build` (or the turn assembler) calls the gate with the registered spec and empty arguments before the request is built, and maps the verdict to the declaration — `Deny` drops the tool, `Ask` sets the provider approval flag when `Caps.ProviderTools[name].Approval` is true and otherwise drops it, `Allow` declares it. Ordering inside the gate is unchanged (D6). Rationale: the arguments arrive after the fact, so the only pre-call input is the spec; running the gate on the spec is exactly what `tools` rule 2 states. A separate "provider gate" that re-implemented ordering would fork D6, so the change reuses the existing gate and adds an entry point.

### D3 — Approval is a suspension, not an inline wait

When the profile supports provider approval and the verdict is `Ask`, the request is declared with the approval flag. When the provider returns an approval request, the run suspends with `HumanApproval` and an `ApprovalRequest` built from the pending call — including `ArgOrigins` from the taint hook when the pre-call gate produced them. `Resume(Approve())` replays the model call with the approval item under the original `CallKey`. Rationale: reuses the existing suspension and replay machinery; an inline block inside the provider iterator would violate the iterator contract in `model` and lose durability.

### D4 — Provenance conversion

The adapter converts the provider's call block to a `ToolUse` and its result block to a `ToolResult`; every appended block carries `OriginProvider{Name}` of the registered spec with that name (not of a block field). Opaque payloads the provider requires back (encrypted search content, citations) stay inside the result as `Raw` and round-trip per `model` rule 8; a provider tool block is never stored as `Raw`. This conversion lives in the shared path the conformance suite exercises, so both adapters inherit it. Rationale: `taint` rule 1 reads block origins; a result without `OriginProvider` would be laundered into trusted content.

### D5 — Post-hoc taint check

The pre-call gate cannot see arguments, so the post-hoc check is the enforcement point. When the response arrives, the implementation runs the `taint` rule-1 computation over the arguments the provider reported for each declared provider tool, before any result block is appended or used. A tainted input into an `Exfil` provider tool fails the run `Failed(Permanent)` and increments `gohan.taint.post_hoc{tool}`. A provider tool with `Exfil` is both the exfil and the untrusted legs of the build check (`taint` rule 3); `Build` must already count it, which is a `Build`-side obligation this change wires (the exfil derivation itself is D5 of the milestone, `openspec/changes/m1/design.md:238`).

### D6 — Usage accounting is the model capability's

The adapter counts each provider tool call it observes in a response under the tool's name in `Usage.ProviderToolCalls`. The run cost is computed by the existing `charge` path (`core/chains/limits.go:142-144`), which multiplies by `Pricing.ProviderCall[name]`; this change adds the fill, not a second pricing path. Each provider call counts toward `MaxToolCalls` and is journaled `Completed` directly (never `Reserved`), per `tools` rule 5. Rationale: `Usage` is the model capability's record and the adapters are the only place the calls are observable.

### D7 — Dependency on the adapter changes

`m1-provider-conformance-contract` owns the shared suite and the fixture shapes; `m1-openai-provider` and `m1-anthropic-provider` implement the conversion (D4), the usage fill (D6) and the wire encoding. This change does not specify adapter internals; it specifies the governed seams those adapters must call. Recorded per the matrix rows for `tools.provider-*` and `model.provider-tool-usage` (`openspec/changes/m1/design.md:61-64`, `:98`).

## Risks / Trade-offs

- Pre-call gate cannot see arguments → taint for provider tools is post hoc only (D5); a tainted query is spent before the run aborts. Accepted — it is what `taint` rule 7 and `tools` rule 3 state; the abort is before any result is used.
- A provider that returns no usage leaves `ProviderToolCalls` empty → cost undercounts. Mitigation: the adapter counts observed calls, not reported ones; if it cannot observe them it must not claim them.
- Declaration decision cached across turns could declare a tool the current principal may not use. Mitigation: the gate runs per request assembly, not once per run.
- `std/tool/provider` is new surface; an untrusted default with `Exfil: true` must keep the import cap from silently raising effect (`tools.untrusted-effect-cap`). Mitigation: the shipped `ToolPolicy` raises `MaxEffect` explicitly, as the spec states.

## Open questions

- Exact call-site names for the pre-call gate invocation and the post-hoc check (`Build` vs the turn assembler) are unbound until the provider adapter packages exist; the adapter changes name them. This does not change the specs or the task breakdown.
