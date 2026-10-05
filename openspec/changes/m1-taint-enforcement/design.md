# Design

## Context

M0 declares `TaintAction`, `ArgTaint`, `TaintDenied`, and `TaintHook`; `TaintHook` has signature `func(ctx context.Context, spec ToolSpec, args jsontext.Value) (TaintAction, []ArgTaint)` (`core/types/taint.go:9-40`). The permission gate exposes `WithTaintHook` and runs the hook before grants and the decider, but no policy hook is installed by a flow definition (`core/permission/gate.go:79-92,158-185`; `core/native_options.go:16-28`).

The `taint` contract requires string-argument matching over untrusted raw session blocks since the latest user message, policy action by capability/effect, and a per-flow Build trifecta (`openspec/specs/taint/spec.md:42-55`). The existing `TaintPolicy` normative shape lacks `MaxWindowBytes` even though the rule requires it (`openspec/specs/taint/spec.md:28-42,46`). ADR-0155 resolves that mismatch and fixes the native registration seam and ownership boundaries (`docs/adr/0155-m1-taint-and-egress-contract.md:17-29`).

## Goals / Non-Goals

**Goals:**
- Implement the 11 scenario IDs assigned to this change: four computation cases, four gate cases, and three trifecta cases (`openspec/changes/m1/design.md:33-47`).
- Make the policy and matcher owned by `std/taint`, while core owns only the hook slot (`openspec/changes/m1/design.md:230-232`).
- Specify integration signatures, Build failures, ordering, test fixtures, and dependency on egress classification before implementation admission (`openspec/changes/m1/design.md:262-297`).

**Non-Goals:**
- Extraction provenance, provider-executed tool post-hoc enforcement, and their scenarios remain owned by later M1 changes (`openspec/changes/m1/design.md:48-50`).
- Egress policy evaluation and derivation are not duplicated here; the egress change owns those operations (`openspec/changes/m1/design.md:234`).
- No second `agent.WithTaintPolicy` builder is added; that illustrative name is not shipped (`docs/adr/0155-m1-taint-and-egress-contract.md:23`).

## Decisions

### D1 — Taint window limit

The normative `TaintPolicy` gains `MaxWindowBytes int` after `MinMatch`. A zero value selects 256 KiB. When the raw session window exceeds the limit, drop oldest blocks first and increment `gohan.taint.window_truncated` (`openspec/changes/m1/design.md:230`; `docs/adr/0155-m1-taint-and-egress-contract.md:19`). This reconciles the normative rule with the struct; do not hardcode a non-configurable cap (`openspec/specs/taint/spec.md:28-33,46`; `docs/adr/0155-m1-taint-and-egress-contract.md:41-42`).

### D2 — Policy ownership

`std/taint` owns `TaintPolicy`, defaults, `Default()`, substring matching, and construction of a `types.TaintHook`. Core keeps the hook slot and does not import policy values or matcher logic (`openspec/changes/m1/design.md:231`; `docs/adr/0155-m1-taint-and-egress-contract.md:21`).

### D3 — Native definition registration

Add `TaintPolicy types.TaintHook` to `gohan.NativeSpec`. Resolve and wire it during `Build` in the same way as `Decider`, using the existing `WithNativeAgent(spec NativeSpec) Option` registration seam. A nil hook preserves current no-enforcement behavior. Do not add a parallel builder (`core/native_options.go:16-38`; `docs/adr/0155-m1-taint-and-egress-contract.md:23`; `openspec/changes/m1/design.md:232`).

### D4 — Build trifecta

For each resolved flow, determine whether registered tools/providers include at least one Exfil capability, at least one PrivateRead capability or provider, and at least one untrusted content path. If all three are present and the flow has no declared hook, fail Build with `ErrTaintTrifecta{Flow, Exfil, PrivateRead, Untrusted}`. Record the state in `gohan.build.trifecta{flow}` (`openspec/changes/m1/design.md:233`; `docs/adr/0155-m1-taint-and-egress-contract.md:25`). A no-trifecta flow builds without a hook (`openspec/specs/taint/spec.md:111-124`).

### D5 — Single Exfil classification owner

The egress change owns `Capabilities.Exfil` derivation. Build calls `RequiresEgress`, `CheckEgress`, and `DeriveExfil` for each registered tool and writes the derived value into the resolved spec. The taint hook reads `ToolSpec.Capabilities.Exfil`; it never derives Exfil again (`openspec/changes/m1/design.md:234`; `docs/adr/0155-m1-taint-and-egress-contract.md:27`). Taint implementation depends on the egress change landing first for end-to-end Exfil enforcement. Tests of the matcher and gate may inject an already-resolved `ToolSpec` capability; they must not implement a second classifier.

### D6 — Permission precedence

The shipped gate checks scopes, then runs the taint hook, then the grant, then the decider (`core/permission/gate.go:158-185`); a live deny implemented as a decider verdict would therefore run *after* a taint `Ask` and lose. The seam this change lands is `permission.WithLiveDeny(func(ctx context.Context, inv *ToolInvocation) (reason string, denied bool))`, consulted inside `decideCall` after the missing-scope check and before the taint hook; a nil lookup means no live deny, exactly as a nil hook means no taint enforcement. A deny returns `Decision{Verdict: DenyVerdict, Reason: reason}`, which the batch gate already projects onto `runtime.BatchDeny`. Flag evaluation happens once per tool call, which is what the `flags` contract states for a `KillSwitch`, so no post-hook recheck is added.

`m1-flags-control` supplies the flags-backed lookup and owns `flags.stale-then-deny` and `flags.never-widen`; this change owns the ordering, the option, and the proof that a deny bound before a call beats a taint `Ask` and requests no approval (`openspec/specs/flags/spec.md:42`; `openspec/changes/m1/design.md:235`; `docs/adr/0155-m1-taint-and-egress-contract.md:29`).

Keep taint `Ask` outside grant and decider widening: the hook runs before `grantCheck`, so a session grant cannot satisfy a tainted call.

### D7 — Raw session window

Compute the window from unprojected session blocks since the last `OriginUser` message. Projection and compaction must not remove provenance from taint computation. Apply D1's byte cap to this input (`openspec/changes/m1/design.md:236`; `openspec/specs/taint/spec.md:46`). The session path must pass raw blocks into the hook computation; do not use a projected view as a substitute.

### D8 — Hook input and session history

The shipped hook signature receives only `context.Context`, `types.ToolSpec`, and `jsontext.Value`; it receives no session or history argument (`core/types/taint.go:37-40`). The gate receives `types.RunInfo` from its run lookup (`core/permission/gate.go:51-71,100-103`). The stack owns the configured `stores.SessionLog` (`core/conversation.go:60-64,141-145`), and `stores.SessionLog.Load(ctx, sessionID)` returns `stores.History` (`core/stores/session_memory.go:156-166`).

Add these core context accessors in `core/types/taint.go`:

```go
func WithTaintWindow(ctx context.Context, blocks []Block) context.Context
func TaintWindowFrom(ctx context.Context) ([]Block, bool)
```

The window is bound once, in `turnEnv.effectCtx` (`core/native_effects.go:66-70`): that method already runs at every effect entry and installs the turn env, and this change makes it also bind `types.WithTaintWindow(ctx, rawWindow(env.msgs))`. `env.msgs` is the run's live raw message slice at that moment (`core/native_effects.go:48-53`), so both gate invocation paths see the window without any store access: the batch path, where the driver hands the gate to `runtime.Batch{Gate: gate}` (`core/drive_turn.go:644`) and the runtime calls it at `core/runtime/runtime_batch.go:82`, and the resumed-ask replay, which `batchEffect` enters through the same effect context (`core/drive_turn.go:569`) and which calls `gate(ctx, head)` at `core/drive_turn.go:789`. `Send` and `Resume` reach the same gate closure, built once per run by `nativeBatchGate` (`core/native_conversation.go:48,78,122`), so one binding site covers both entry points.

`rawWindow` converts messages to `[]types.Block` — the blocks the model itself saw, unprojected, origins intact — and selects those after the last `OriginUser` message.

`core/permission` reads only the accessor and imports no store; `std/taint` imports no store either. When the accessor reports no window, the hook returns `types.TaintAllow` and no taints. It MUST NOT use projected history as a fallback, and the mid-run rule is: the window is the run's own raw messages at the call, so a steer or user message that arrives mid-run enters the window at the next turn rather than retroactively, which is exactly the boundary the model saw.

Rejected: putting store access in `std/taint` or widening `types.TaintHook`. The former violates the package boundary; the latter changes the shipped hook signature and still requires a session source (`docs/design/architecture.md:45-47`; `core/types/taint.go:37-40`).

### D9 — Matcher API and boundaries

In `std/taint/taint.go`, declare:

```go
type TaintPolicy struct {
    ToExfil types.TaintAction
    ToSideEffect types.TaintAction
    ToReadOnly types.TaintAction
    MinMatch int
    MaxWindowBytes int
}
func New(policy TaintPolicy, tel types.Telemetry) (types.TaintHook, error)
var ErrInvalidPolicy = errors.New("taint: invalid policy")
```
`func Default() TaintPolicy` returns `ToExfil: types.TaintDeny`, `ToSideEffect: types.TaintAsk`, `ToReadOnly: types.TaintAllow`, `MinMatch: 16`, and `MaxWindowBytes: 256*1024` — the defaults the capability spec already states (`openspec/specs/taint/spec.md:42`). The only constructor of a hook is `New(policy TaintPolicy, tel types.Telemetry) (types.TaintHook, error)`; there is no separate `Hook` function, and `std/taint` exports no other entry point.

The matcher's declared surface, so no implementer widens or narrows it by accident: an argument is tainted when its whole value, or a substring of at least `MinMatch` bytes, occurs verbatim in an eligible block's text, tool-result content or document content; eligible blocks are the raw blocks after the last `OriginUser` message whose `Origin.Kind` is neither `OriginSystem` nor `OriginUser`. Shorter embedded fragments are deliberately not detected — they are below the false-positive floor the capability accepts — and a projected, compacted or tool-result-cleared view is never the input. `New` validates actions and requires `MinMatch >= 1` and `MaxWindowBytes >= 0`; it returns `ErrInvalidPolicy` for invalid values. Its returned hook walks JSON arguments recursively and returns every matching argument with matched origins. An exact whole-value match qualifies at any length. A substring match qualifies only when it has at least `MinMatch` bytes. Therefore `"42"` does not match inside a longer value by substring. An exact `"42"` block match still qualifies.

Count each block's text bytes in chronological order. If the total exceeds the effective cap, remove oldest blocks until the retained blocks fit. A zero cap means 262144 bytes. Record `gohan.taint.window_truncated` once per hook evaluation that removes blocks.

`TaintPolicy` comes from `std/taint`; the similarly named `NativeSpec.TaintPolicy` field holds the resulting hook (`openspec/changes/m1/design.md:231-232`; `docs/adr/0155-m1-taint-and-egress-contract.md:21-23`).

### D10 — Native wiring and Build error

Add `TaintPolicy types.TaintHook` to `gohan.NativeSpec` in `core/native_options.go`. During `Build`, `resolveNative` stores it in `resolvedNativeConfig`; native turn wiring passes it to the permission gate through `permission.WithTaintHook` (`core/native_options.go:16-38`; `core/native_resolve.go:37-86`; `core/native_conversation.go:122-140`, where `nativeBatchGate` builds the gate per run; `core/permission/gate.go:79-84`). Despite its name, the field holds the already-built flow hook, as D3 requires (`openspec/changes/m1/design.md:232`; `docs/adr/0155-m1-taint-and-egress-contract.md:23`).

Add `types.ErrTaintTrifecta` in `core/types/errors.go`:

```go
type ErrTaintTrifecta struct {
    Flow string
    Exfil bool
    PrivateRead bool
    Untrusted bool
}
```

Its `Error()` format is `gohan: flow "<Flow>" requires a taint policy (exfil=<Exfil>, private_read=<PrivateRead>, untrusted=<Untrusted>)`. This typed Build error does not cross a transport, so it does not need a Problem code (`core/types/problem.go:10-13,251-255`).

Build computes three booleans per resolved flow: any derived `ToolSpec.Capabilities.Exfil`; any private-read tool or provider; and any untrusted tool, skill, Document provider, AwaitingInput/frontend path. When all three are true and the resolved hook is nil, `Build` returns this error before returning the Stack or making a provider call. Emit `gohan.build.trifecta` with `flow` label and value 1 when all legs are present, otherwise 0 (`openspec/changes/m1/design.md:233`; `docs/adr/0155-m1-taint-and-egress-contract.md:25`).

### D11 — Denial, Ask, telemetry, and live denies

The gate applies hard/live denial before taint. A taint `Ask` becomes `permission.AskError{Invocation: inv}`; the run layer maps it to `HumanApproval`, and the taint values on `ToolInvocation.Taints` populate `ApprovalRequest.ArgOrigins` (`core/permission/gate.go:39-49,51-59,165-173`; `openspec/specs/taint/spec.md:89-107`). Deny returns the shipped `types.TaintDenied{Arg, Origins}` as a `types.ToolError{Kind: types.Permanent}` without calling the tool (`core/types/taint.go:27-35`; `core/permission/gate.go:262-273`). Emit counters `gohan.taint.denied` and `gohan.taint.asked`, each with the `tool` label. Use the existing `types.Telemetry` port supplied to the hook (`core/build_options.go:135-143`).

Concrete proofs:

- Tainted SideEffect argument plus an existing grant → `HumanApproval` suspension, `ApprovalRequest.ArgOrigins` contains the argument origins, and the effect counter stays zero until resume approval.
- Live emergency deny plus taint `Ask` → hard denial and no `HumanApproval` suspension.
- PrivateRead tool plus Untrusted tool plus Exfil tool without a declared hook → `Build` returns `ErrTaintTrifecta` with all three booleans true; model-call counter stays zero.

These proofs use the public native Build/Send/Resume path, not a matcher-only test (`openspec/changes/m1/design.md:295-297`).

### D12 — Normative type update boundary

The delta changes requirements only. When this change applies, update the main taint Contract `TaintPolicy` with `MaxWindowBytes int` after `MinMatch` and regenerate `docs/design/types.md`. ADR-0155 section 1 requires that sequence (`docs/adr/0155-m1-taint-and-egress-contract.md:17-19,34`; `openspec/project.md:18-27`). This authoring package does not edit either shared file.

### D13 — Extraction and provider ownership

`taint.quarantine-carries-origin` and `taint.subflow-carries-origin` remain with `m1-extraction-provenance`; its design must resolve the subflow milestone conflict before implementation admission (`openspec/changes/m1/design.md:48-50,73-80`). `taint.provider-tool-post-hoc` remains with `m1-provider-tool-governance` (`openspec/changes/m1/design.md:50`). This package does not claim or implement those IDs.

## Integration contract

- `std/taint/taint.go` owns `TaintPolicy`, `Default() TaintPolicy`, `New(policy TaintPolicy, tel types.Telemetry) (types.TaintHook, error)` and `ErrInvalidPolicy`; `New` is the only constructor of a hook.
- `core/types/taint.go` owns `WithTaintWindow(ctx, blocks) context.Context` and `TaintWindowFrom(ctx) ([]Block, bool)`; the driver binds them in `turnEnv.effectCtx` (`core/native_effects.go:66`) over `env.msgs`, and the batch gate call sites (`core/runtime/runtime_batch.go:82`, `core/drive_turn.go:789`) inherit that context.
- `core/permission/gate.go` owns `WithLiveDeny(func(ctx, inv *ToolInvocation) (string, bool))`, consulted after the missing-scope check and before the taint hook.
- `core/native_options.go` owns `NativeSpec.TaintPolicy types.TaintHook`; `core/native_resolve.go` resolves it into `resolvedNativeConfig`; `nativeBatchGate` (`core/native_conversation.go:122-140`) is where the gate is built per run and where `permission.WithTaintHook` is passed.
- `core/types/errors.go` owns `ErrTaintTrifecta` and its exact `Error()` format in D10. It does not need a Problem code because Build errors return to the Build caller, not a transport (`core/types/problem.go:10-13,251-255`).
- Egress derivation is a blocking dependency for end-to-end Exfil behavior. `Build` calls `RequiresEgress`, `CheckEgress`, and `DeriveExfil` for each registered tool and the hook reads only the resulting `ToolSpec.Capabilities.Exfil` (`openspec/changes/m1/design.md:234`; `docs/adr/0155-m1-taint-and-egress-contract.md:27`).

## Test and implementation chunks

1. **Raw-window seam and matcher.** Add the context accessor and history binding. Implement policy, defaults, recursive matching and truncation metric. Seed session history with `stores.MemorySessionLog.Append` and block origins; test boundary values, nested JSON, user-turn reset and oldest-first truncation (`core/stores/session_memory.go:79-83,156-209`; `core/types/message.go:27-59`; `openspec/specs/taint/spec.md:62-80`).
2. **Permission path.** Wire the hook into native execution and test public Build/Send/Resume. Seed a tainted side-effect call with an existing grant; assert HumanApproval, ArgOrigins, and no effect before approval. Test ReadOnly allow, Exfil deny and live deny precedence (`core/permission/gate.go:158-185`; `openspec/specs/taint/spec.md:82-107`).
3. **Trifecta.** Add typed error and Build check before Stack return. Register native tools/providers for every leg and assert error fields, exact message, no provider calls, policy success, no-Exfil success, and metric `gohan.build.trifecta` labeled `flow` (`core/build.go:106-159`; `core/native_resolve.go:37-86`; `openspec/specs/taint/spec.md:109-124`).
4. **Egress integration.** After `m1-egress-policy`, exercise Build-derived Exfil values. Do not add a taint classifier. Run assigned scenario tests through the public flow (`openspec/changes/m1/design.md:234,295-297`).

## Risks / Trade-offs

- [Risk] The shipped hook has no history argument → add a core context seam and bind raw session blocks in gate wiring (`core/types/taint.go:37-40`; `core/permission/gate.go:51-71`).
- [Risk] Projection or compaction removes provenance → load raw history rather than projected context (`openspec/changes/m1/design.md:236`; `openspec/specs/taint/spec.md:46`).
- [Risk] Separate Exfil classifiers diverge → consume Build-resolved capability and sequence after egress (`openspec/changes/m1/design.md:234`).
- [Risk] Applying taint after grants allows bypass → preserve hook-before-grant ordering (`core/permission/gate.go:158-185`).

## Migration Plan

1. Apply the main-spec `MaxWindowBytes` edit and regenerate `docs/design/types.md` with the normative ADR record (`docs/adr/0155-m1-taint-and-egress-contract.md:17-19,34`).
2. Implement egress derivation before end-to-end Exfil enforcement (`openspec/changes/m1/design.md:234`).
3. Enable taint for declared hooks and reject undeclared trifecta flows during Build (`docs/adr/0155-m1-taint-and-egress-contract.md:23-27`).
4. Roll back by removing the policy declaration or reverting the implementation; do not leave a declared policy with enforcement disabled (`docs/adr/0155-m1-taint-and-egress-contract.md:35-37`).

## Open Questions

None. The raw-window context accessor is specified above because the shipped hook signature has no session access. Its implementation remains a prerequisite, not an unresolved design choice.

Verification commands for implementation chunks: run `task lint` and `task test` first. Then run `go test -short -timeout 2m ./... -run '<TestName>'` with each focused test name. Run `task spec:gate GATE=M1` for milestone acceptance. After `m1-egress-policy` lands, run the assigned Exfil scenario against Build-derived capabilities. Validate this package with `openspec validate m1-taint-enforcement --strict --no-interactive`.

