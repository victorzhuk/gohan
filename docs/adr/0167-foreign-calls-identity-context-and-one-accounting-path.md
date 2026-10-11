# 0167. Foreign calls carry identity and context and share one accounting path

Status: accepted

## Context

The foreign tool path was built as a composition of native pieces that did not
line up. `foreignTools` composed only the tool chain, while the approval gate and
the effect journal lived at the native effect boundary, so a foreign tool call
reached governance nowhere. `governedTool.Call` built `types.ToolUse{Name, Args}`
with no `ID` even though `types.ToolUse` carries one, so no foreign call had a
journal key and two calls to the same tool in one turn were indistinguishable.
`foreignAgentRun` derived its context from `context.Background()`, dropping the
caller's principal, credential, run info, deadline and cancel chain.
`resolveForeign` called `resolveNative`, which assigns `s.native = resolved`
whenever the map is nil, so a second resolution replaced the registry and dropped
every native entry. The recovery drive wrapped a second ledger over the one the
foreign tools charged, and the resumed drive re-seeded its reservation from the
parked queue rather than the run's admitted total, so one call could be metered
twice or not at all.

## Decision

A foreign call is refused before it reaches any governance machinery if it
carries no call id. `types.ErrForeignCallID` is the sentinel, declared in
`core/types/errors.go` beside the other tool errors, and the carrier crosses the
adapter boundary in a `core/types` context value: `types.WithToolUse` and
`types.ToolUseFrom`, declared beside `WithRunInfo`/`RunInfoFrom` because the
governed check lives in `core`. `adapter/eino` binds `ToolContext.CallID` through
`NewToolCallIDHandler` and `WithToolCallID`; it installs nothing itself, because
the caller builds the agent and passes it in. `turnTool.InvokableRun` still calls
`types.Tool.Call(ctx, args)` unchanged — `types.Tool.Call` has no call
parameter, and no replacement signature is introduced.

`governedTool.Call` reads the carrier, builds the authoritative `types.ToolUse`
from the carried `ID`, the registered tool's `Spec().Name` and the received args,
and rejects a missing or empty `ID` before the gate, the journal, the reservation
and the execution. The refusal is
`types.ToolResult{ID: "", Outcome: types.Failed, Error: &types.ToolError{Kind:
types.Permanent, Message: runtime.NotExecutedPrefix +
types.ErrForeignCallID.Error()}}`. A backend that supplies no call id therefore
fails loudly instead of running ungoverned, and no id is synthesised from a tool
name and a counter — a synthesised id collides across turns and breaks the
resumed-call match.

Governance is not re-implemented for foreign calls; it is shared.
`governedForeignCall` runs `runtime.Batch` over the single call with
`governedAskExec` as `Exec`, so the gate decision, the `not_executed:` denial
shape, the `types.SuspendError{Reason: types.HumanApproval}` on `BatchAsk`, the
rejected-completion validation, the telemetry span, the `ToolStarted` sink event
and `types.Origin` provenance all come from the same code the native path runs.
The gate is built from `nativeBatchGate` over the same resolved configuration and
history, so taint, hard blocks, session grants and the decider apply on the
native terms. A foreign run does not receive native batch scheduling — `Step`
stays turn-granular — and it does not grow a second copy of the sequence.

The composed tool chain stays the only ordinary-tool journal wrapper.
`foreignTools` keeps `governedToolExec` around the resolved chain, so a declared
`std.Journal` still reserves and completes exactly as it does for a native call,
and `std.Shield` still governs the effect. An outer reserve/complete wrapper
around that chain would journal one call twice, and the provider-executed journal
block belongs to calls a provider executed outside the tool chain — an ordinary
foreign tool call goes through the chain, so that block is not copied onto this
path.

The accounting is charged once. Each newly admitted call reserves one slot through
the run-local `(*chains.LimitsState).ReserveBatch`; a refusal refunds through the
returned release, and the reservation context reaches the call so an inner charge
consumes the prepaid slot through `(*chains.LimitsState).ConsumeReservation`
instead of charging twice. `foreignAgentRun` builds the run's `*chains.LimitsState`
and installs it on the derived context, and the recovery drive drives on the
ledger `foreignAgentRun` returns rather than wrapping its own — the dropped
ledger was never the one the foreign tools charged. The resumed drive seeds its
recharge from the run's cumulative admitted count with the parked calls' own ids,
so an approved replay spends exactly the slots the first drive admitted and the
budget is charged once across both drives. `Send`, `Resume` and `Recover`
therefore meter one ledger.

Context lineage comes from the caller.
`foreignAgentRun(ctx, sessionID, name, hist, seed, input)` derives the run context
from `ctx` rather than `context.Background()`, so `Principal`, `Credential`,
`RunInfo`, the deadline and the cancel chain survive into every foreign component
call. Identity values are never re-derived; only lineage changes. Lineage is not
object identity: `std.Shield` deliberately hands a `SideEffect` call
`context.WithoutCancel(ctx)`, so the same `context.Context` object is not promised
inside a shielded call.

Cancellation is honoured by effect, with the shipped `std.Shield` semantics as the
bound. A `ReadOnly` call is not shielded, so cancellation reaches it and the call
aborts; a non-`ReadOnly` call that began its committed execution keeps the
shield, settles, has its result journalled, and only then does cancellation stop
the components after it. Cancellation never discards a committed result before
journal settlement, and `ReadOnly` gains no journal requirement it did not have.
`Recover` answers a completed non-`ReadOnly` call from the journal and does not
re-execute it.

Foreign resolution merges. `resolveForeign` adds its entries to the existing
resolved registry rather than assigning a fresh map, keeping the duplicate-name
check that fails `Build` when a foreign name is already native. A genuine
collision still fails the build; a separate private resolution path is rejected
because it duplicates the resolution logic and can drift.

Approval on a foreign suspension runs through the same `Resume` a native
suspension uses, so the scoped approval is stored on the same terms. That is the
decision recorded in [0162](0162-the-grant-contract-is-declared-by-core-permission.md):
`permission.Grant` and `permission.GrantStore` are declared by `core/permission`,
`Build` binds the store through `ApprovalGrants`, `ApproveScope(ttl)` returns
`stores.ResumeInput{Verdict: stores.VerdictApprove, ScopeTTL: ttl}`, and the TTL
is capped by the bound store's policy rather than by a driver default. A resume
carrying `ScopeTTL > 0` with no bound grant store fails closed with
`std/permission.ErrGrantRefused` and executes nothing. This record does not
restate that decision and makes no change to it; it records only that a foreign
run consumes it.

This record adds no capability and no identifier the delta did not already
introduce. It fixes what a foreign call must carry before it may execute and
which one code path charges it.