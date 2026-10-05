# 0155. Taint and egress contract for M1

Status: accepted

## Context

M0 shipped the ports and the declarations the `taint` capability needs and none of its enforcement. `core/types/taint.go` declares `TaintAction` (`TaintAllow`, `TaintAsk`, `TaintDeny`), `ArgTaint`, `TaintDenied` and the `TaintHook` slot; `core/permission/gate.go:82` installs a hook through `WithTaintHook`, and `gate.decideCall` runs scope check → hook → grant → decider, which is the order the spec's rule 2 describes. No `std/taint` package exists, so no flow has ever installed a hook.

The egress half is the mirror image: `core/types/tool.go:59` declares `EgressPolicy`, `EgressDenied` and `Capabilities`, `core/types/tool_egress.go` declares `RequiresEgress`, `CheckEgress` and `DeriveExfil`, and `ErrEgressPolicyRequired` is catalogued. Nothing calls them from `Build`; their only coverage is `core/types/tool_egress_test.go`, which calls them directly. There is no `WithEgress`, `WithExfil` or `WithPrivateRead` tool option, no `std/egress` client and no `std/tool/http`.

Three gaps between the `taint` spec and the code need a decision before M1 implementers can work, because both the egress change and the taint change would otherwise answer them separately:

1. Rule 1 caps the untrusted window at `TaintPolicy.MaxWindowBytes` (default 256 KiB), but the `TaintPolicy` struct in the spec's Contract block has no such field. The struct is normative; the rule that reads the field is normative. One of them is wrong.
2. The spec states the policy is installed with `agent.WithTaintPolicy`, a constructor name no shipped code has. The shipped registration seam is `gohan.WithNativeAgent(NativeSpec{...})` plus `Build` options (`core/native_options.go:16`, `core/build_options.go`).
3. `Capabilities.Exfil` is derived from the egress policy by `DeriveExfil`, while the `taint` spec's own text also classifies a tool with no policy as `Exfil: true` when it is untrusted, sandboxed with egress, or wired `WithExfil()`. Two packages could implement two classifiers.

## Decision

1. **`TaintPolicy` grows `MaxWindowBytes int`.** The field is added to the normative struct, positioned after `MinMatch`. `0` means the default window of 256 KiB; the truncation drops the oldest blocks first and counts `gohan.taint.window_truncated`. Rule 1 is the authority for the field's behaviour; the struct catches up.

2. **Policy values stay in `std/taint`, the hook slot stays in core.** `std/taint` declares `TaintPolicy`, its defaults, the substring matcher and `Default()`, and returns a `types.TaintHook`. Core keeps declaring the slot only. The core budget rule (`docs/design/architecture.md` §4.2a) is unchanged: no policy value and no matcher in `core`.

3. **The policy is installed through the native definition.** `NativeSpec` gains a `TaintPolicy` field holding the flow's hook, resolved by `Build` exactly like `Decider`, and the resolved hook is what the flow's gate is wired with. A flow that declares no hook keeps today's behaviour — the gate skips taint enforcement. `agent.WithTaintPolicy` is illustrative-tier naming and is not implemented; no second builder is introduced to preserve it.

4. **`Build` performs the trifecta check.** Per resolved flow, `Build` computes the three legs from the registered tools and providers — at least one `Exfil` capability, at least one `PrivateRead` capability or provider, and at least one untrusted content path — and fails with `ErrTaintTrifecta{Flow, Exfil, PrivateRead, Untrusted}` unless the flow declared a policy. A flow with no trifecta builds without one. `gohan.build.trifecta{flow}` records the state.

5. **Egress owns `Capabilities.Exfil` derivation; taint consumes it.** `Build` calls `RequiresEgress`, `CheckEgress` and `DeriveExfil` for every registered tool and writes the derived capability back into the resolved spec, so a later consumer reads one value. The taint hook never re-derives exfil-capability from registration; it reads `ToolSpec.Capabilities.Exfil`. The taint spec's sentence about untrusted and sandbox tools without a policy stays, and is implemented as the derivation input, not as a second derivation.

6. **Permission order is fixed.** Hard and live denies, then the taint hook, then session grants, then the decider. A live deny that arrives while a taint verdict is `Ask` wins, and no approval is requested (`taint.gate-order`).

## Consequences

- M1's two first changes touch one shared vocabulary: the egress change wires `Build`'s derivation and refusal, the taint change wires the hook and the trifecta check. Neither re-derives the other's classification.
- The `taint` spec's Contract block and rule 1 must be edited together with this ADR, and `docs/design/types.md` regenerated, before the implementing change can pass `task spec:types`.
- A flow that declares a policy becomes taint-enforced, which changes behaviour for tools whose arguments copy untrusted content: such calls now deny or ask where they previously passed. This is the point of the capability and is listed under *Breaking* when the change lands.
- Because the hook is resolved at `Build` from the definition, `Explain` can project the policy it will enforce without spending a run budget.
- The window default is sealed at 256 KiB; a smaller window is a policy choice an author makes explicitly, and truncation is always visible in `gohan.taint.window_truncated` rather than silent.

## Alternatives

- Leaving `MaxWindowBytes` out and hardcoding the window in the matcher. Rejected: rule 1 names the field and the metric reports truncation; a constant would make the cap unconfigurable per flow while the spec text told authors it was.
- Implementing `agent.WithTaintPolicy` as a second builder. Rejected: `agent` is a package that does not exist; the native definition is the only shipped public construction seam, and two seams for one policy is the divergence this ADR exists to prevent.
- Deriving `Capabilities.Exfil` inside the taint matcher from the egress policy. Rejected: the matcher runs per call inside the gate; derivation is a build-time property of the tool and `DeriveExfil` already implements it.
- Making the trifecta check a `std` helper the flow author calls. Rejected: the requirement is that a flow meeting all three legs fails `Build` unless it declares a policy — a helper a caller may forget fails nothing.
- Enforcing taint after the grant check. Rejected: a session grant approved once would then authorise every later tainted call with the same fingerprint, which is the hole the capability closes.
