# Design: restore-runtime-governance

## Context

See `proposal.md` - Why. The current state that shapes the approach:

- `resolveForeign` resolves foreign definitions by calling `resolveNative` (`core/foreign_conversation.go:49`), which assigns `s.native = resolved` when the map is still nil (`core/native_resolve.go:176-179`), so a second call replaces the registry.
- `foreignTools` composes only the tool chain: `governedToolExec(cfg.toolChain, …)` (`core/foreign_conversation.go:109-118`). The native approval gate and effect journal live at the effect boundary the native batch path drives, not in the tool chain (`core/native_effects.go`, `openspec/specs/runtime/spec.md` *Tool batch protocol*).
- `governedTool.Call` builds `types.ToolUse{Name, Args}` with no `ID` (`core/foreign_conversation.go:125-134`). `types.ToolUse` carries `ID` (`core/types/message.go:136-141`).
- `foreignAgentRun` derives its context from `context.Background()` (`core/foreign_conversation.go:62-73`).
- The eino backend exposes the call id as `ToolContext{Name, CallID}` to each handler in `ChatModelAgentConfig.Handlers` (`adk/handler.go:44-48`, `adk/chatmodel.go:311-321`), filled from `compose.ToolInput.CallID` (`adk/wrappers.go:126-129`); `tool.Option` carries only implementation-specific option functions (`components/tool/option.go:22`). The bridge's `turnTool.InvokableRun` ignores it (`adapter/eino/runtime.go:358-380`), while `toSchemaMessages` maps `types.ToolUse.ID` to `schema.ToolCall.ID` (`adapter/eino/runtime.go:419-448`).

## Goals / Non-Goals

**Goals:**

- One governed per-call path shared by native and foreign tool calls: gate, journal, cancel shield.
- A stable call identity that survives the foreign boundary and matches the journal key on resume and recovery.
- The caller's context, identity, deadline and cancellation reach every foreign component call.
- Foreign resolution preserves every native registration.

**Non-Goals:**

- Does not give foreign runtimes native batch scheduling; `Step` stays turn-granular (`openspec/specs/runtime/spec.md` *Tool batch protocol*).
- Does not change `Send`'s signature or detached behaviour, nor any lifecycle constructor common field (`repair-conversation-lifecycle`).
- Does not own imported-tool construction or message fidelity (`m2-eino-bridges`).
- No retry or fallback framework; a missing call id fails closed.

## Decisions

### D1. The core effect boundary owns the foreign per-call governance

The core effect boundary already settles one governed call per batch slot: `runtime.Batch.Run` decides every call before the first executes (`core/runtime/runtime_batch.go:42`), `governedAskExec` (`core/drive_turn.go:1027`) runs it, renders the failure shapes and sets provenance, and the effect converts a suspension at its own boundary (`core/drive_turn.go:887`). A foreign run reuses that machinery. It does not grow a second copy of the sequence and it does not take over the ordinary-tool journal.

- The composed tool chain stays the only ordinary-tool journal wrapper. `foreignTools` keeps `governedToolExec` (`core/native_effects.go:56`), so `std.Journal` and `std.Shield`, when the resolved chain declares them, still reserve, complete and shield exactly as they do for a native call. An outer `Reserve`/`Complete` wrapper around that chain would journal a call twice.
- The provider-executed journal block at `core/drive_turn.go:815-833` belongs to calls a provider executed outside the tool chain. An ordinary foreign tool call goes through the chain, so that block MUST NOT be copied onto the foreign path.

Exact declaration surface at apply time, in `core`:

```go
func governedForeignCall(
	gate runtime.BatchGate,
	reserve func(ctx context.Context, n int) (context.Context, func(), error),
	env *turnEnv,
	exec func(context.Context, types.ToolUse) (types.ToolResult, error),
) func(context.Context, types.ToolUse) (types.ToolResult, error)
```

- One call at a time: `runtime.Batch{Calls: []types.ToolUse{call}, Gate: gate, Exec: governedAskExec}` (`core/drive_turn.go:1027`), so the gate decision, the denial result and the suspension all come from the same code the native path runs.
- Reservation: one slot per newly admitted call through the run-local `(*chains.LimitsState).ReserveBatch(ctx, cfg.limits, 1)` (`core/chains/limits.go:554`). A refusal refunds through the returned release. The reservation context reaches the call, so an inner charge consumes the prepaid slot through `(*chains.LimitsState).ConsumeReservation` (`core/chains/limits.go:537`) instead of charging twice. An approved replay reuses the slot its first drive charged: the resumed foreign wiring recharges the run's already-admitted tool calls once on first reservation, exactly as the native wiring does (`core/native_conversation.go:59-72`).
- `BatchAsk`: preserve the pending call identity and the approval state, then return `&types.SuspendError{Reason: types.HumanApproval}` (`core/drive_turn.go:887`). An ask is never rendered as a permanent tool failure, and the suspension keeps the call id the journal key uses.
- Turn scope: the helper takes the run's `*turnEnv`, so a rejected completion (`validateCompletion`), the telemetry span, the `ToolStarted` sink event and `types.Origin` provenance are set by the shared code path.
- Gate source: a foreign run builds `gate` from `nativeBatchGate` (`core/native_conversation.go:134`) over the same resolved configuration and history, so taint, hard blocks, session grants and the decider are the native terms.
- Alternative rejected: gate inside `foreignTools` with a fresh decider — duplicates the taint, grant and `not_executed:` result shapes that `permission` and `runtime` already own.

### D2. A core-owned context carrier carries the backend call id

The call id crosses the adapter boundary through a `core/types` context carrier, and core owns the fail-closed check. `types.Tool.Call(ctx, args)` (`core/types/tool.go`) has no call parameter, so the id cannot travel as an argument. It travels in the context the backend call already receives, and the core wrapper reads it.

Exact declaration surface at apply time:

```go
// core/types/tool.go
func WithToolUse(ctx context.Context, call ToolUse) context.Context
func ToolUseFrom(ctx context.Context) (ToolUse, bool)

// core/types/errors.go
var ErrForeignCallID = errors.New("gohan: foreign tool call carries no call id")

// adapter/eino/runtime.go
func NewToolCallIDHandler() adk.ChatModelAgentMiddleware
func WithToolCallID(endpoint adk.InvokableToolCallEndpoint, call types.ToolUse) adk.InvokableToolCallEndpoint
```

- Carrier owner: `core/types` owns the key, the setter and the reader, next to `WithRunInfo` and `RunInfoFrom` (`core/types/identity.go:133-149`), because the governed check lives in `core` (`core/foreign_conversation.go:125`).
- Adapter behavior: the eino handler binds `ToolContext.CallID` (`adk/handler.go:44-48`) through the core carrier. `turnTool.InvokableRun` (`adapter/eino/runtime.go:370`) still calls `types.Tool.Call(ctx, args)` unchanged; it does not construct a replacement `Tool.Call` signature.
- Core behavior: `governedTool.Call` reads `types.ToolUseFrom(ctx)`, builds the authoritative `types.ToolUse` from the carried `ID`, the registered tool name (`g.tool.Spec().Name`) and the received args, and rejects a missing or empty `ID` before the gate, the journal, the reservation and the execution.
- Why not a `tool.Option`: `tool.Option` carries only implementation-specific option functions (`components/tool/option.go:22`), so nothing there can carry a call identity core can read.
- Wiring: the handler belongs to the agent the caller builds. `adk.NewChatModelAgent` takes `ChatModelAgentConfig.Handlers` (`adk/chatmodel.go:311-321`), and `adapter/eino.New` receives an already-built agent (`adapter/eino/runtime.go:54`), so the adapter installs nothing and verifies nothing. A handler that was never installed leaves the carrier empty, and the call fails closed.
- Handler wiring: `NewToolCallIDHandler` embeds `*adk.BaseChatModelAgentMiddleware` (`adk/handler.go:277`) and overrides only `WrapInvokableToolCall` (`adk/handler.go:279`), so every other handler method stays the base no-op.
- Exact fail-closed result:

```go
types.ToolResult{
	ID:      "",
	Outcome: types.Failed,
	Error:   &types.ToolError{Kind: types.Permanent, Message: runtime.NotExecutedPrefix + types.ErrForeignCallID.Error()},
}
```

- Exact evidence: `governedTool.Call` builds the call today at `core/foreign_conversation.go:121-127`, and the bridge reads no call identity today at `adapter/eino/runtime.go:359-380`.
- Alternative rejected: synthesise an id from the tool name and a counter — collides across turns and breaks the resumed-call match.

### D3. Derive the foreign run context from the caller

`foreignAgentRun` and the conversation's `foreignRun` closure seed the ledger onto a context derived from the caller's context, not `context.Background()`. The signature is `func (s *Stack) foreignAgentRun(ctx context.Context, sessionID string, name string, hist stores.History, seed float64, input []types.Message) (context.Context, runtime.Runtime, runtime.AgentRun, *chains.LimitsState, error)`, the same declaration `m2-context-persisted-compaction` specifies, so both changes bind the caller's context and the session id on one signature. The derived context keeps `Principal`, `Credential`, `RunInfo` and any deadline, and stays cancellable.

- Why: `Send`, `Resume` and `Recover` all carry the caller context; dropping it also drops the cancel path the `Graceful shutdown` requirement depends on (`openspec/specs/runtime/spec.md`).
- Identity values are never re-derived; only context lineage changes.
- Lineage, not object identity: a derived context preserves the caller's identity values, deadline and cancellation chain. It does not promise the same `context.Context` object inside a shielded call, because `std.Shield` (`std/shield.go:15`) deliberately hands a `SideEffect` call `context.WithoutCancel(ctx)` and reports `context.Canceled` after the effect settled (`std/shield.go:23-25`). A requirement that demanded the same object would contradict the shipped shield.
- Cancellation, by effect: a `ReadOnly` call is not shielded, so caller cancellation reaches it and the call aborts. A non-`ReadOnly` call that already began its committed execution keeps the shield: it settles, the chain journals the result (`std/journal.go:136`), and only then does cancellation stop the components after it. Cancellation MUST NOT discard a committed result before journal settlement, and a `ReadOnly` call MUST NOT gain a new journal requirement it did not have.
- Recovery: `Recover` answers a completed non-`ReadOnly` call from the journal and MUST NOT re-execute it.

### D4. Resolve foreign definitions by merging, not replacing

Foreign resolution must add its entries to the existing resolved registry rather than assign a fresh map, and keep the duplicate-name check that fails `Build` when a foreign name is already native.

- Why: `resolveNative` assigns `s.native = resolved` whenever the map is still nil (`core/native_resolve.go:176-179`); calling it a second time drops native entries. A merge preserves each flow's resolved model, tools, chains and policies.
- Alternative rejected: resolve foreign definitions through a separate private path — duplicates the resolution logic and can drift from native resolution.

### D5. The grant vocabulary moves to `core/permission`; the store and its TTL policy stay in `std/permission`

A foreign run answers `BatchAsk` through the same `Resume` any native suspension uses, so the scoped approval the approver delivers must be stored on the same terms. The grant vocabulary the driver must name is `core` vocabulary, so `Grant` and `GrantStore` are declared in `core/permission/approval.go` beside `ApprovalRequest`, and `Build` binds the store through `ApprovalGrants(store permission.GrantStore) Option` (`core/build_options.go`), which `Stack` and both conversation constructors carry as `approvalGrants`. `ApproveScope(ttl)` stays in `core/suspension.go` and returns `stores.ResumeInput{Verdict: stores.VerdictApprove, ScopeTTL: ttl}`; the TTL travels as `ResumeInput.ScopeTTL` and the grant store's own policy caps it.

- No core-to-std import: `core` and `core/permission` name the interface only. `std/permission` keeps `MaxGrantTTL = 24 * time.Hour`, `GrantedByScope`, `ErrGrantRefused`, `MemoryGrantStore`, `ApproveGrant` and `HasGrant`, so the TTL default and the store implementation stay where the policy already lives.
- No core default policy value: `core` declares no grant TTL, no grant audit kind of its own and no eligibility table. A TTL above the store's cap is the store's decision, and `ApproveGrant` takes `grantedAt time.Time` so `ExpiresAt` derives from the approval instant and never from `req.ExpiresAt`.
- Approver subject, not originator: the grant is keyed on the verifying approving subject delivered on the resume, never on the checkpoint's `Originator`. A separate approver therefore grants the session the authority they hold, and the originator's parked call is the one that executes.
- Fail closed without a store: a resume carrying `ScopeTTL > 0` with no bound grant store returns `std/permission.ErrGrantRefused` and executes nothing, so `ApproveScope` never degrades into an unscoped approval.

### D6. The resume and recovery drives charge one ledger

`foreignAgentRun` builds the run's `*chains.LimitsState` and installs it on the derived context; the governed foreign tools charge it through `reserve`. Two call sites break that.

- Recovery wraps a second ledger: `driveRecovered` builds its own `chains.NewLimitsStateSeeded(run.Cost, ...)` and then calls `chains.WithLimitsState(fctx, ledger)` over the context `foreignAgentRun` returned (`core/recover.go:265-283`). The reservation the foreign tools hold then refers to a ledger the drive does not read. The recovery drive drives on the ledger `foreignAgentRun` returns, exactly as `NewLifecycle(WithLifecycleLedger(ledger))` does for a native re-drive.
- Cumulative admitted count with replay identity: the foreign resume branch seeds the recharge from `len(st.Pending)` (`core/resume.go:390-396`), which is the parked queue, not the run's admitted total. A run that admitted several calls and parked one re-charges one slot instead of the admitted count. The resumed drive carries the cumulative admitted count and the replay identity — the parked calls' own ids — so `admittedToolTotalFrom` restores the slots the first drive admitted and the approved replay spends exactly those, charging the budget once across both drives and executing under the id the journal key names.

Alternative rejected: let each drive re-seed from its own `run.Cost` only. Cost seeding is not call admission; without the admitted count the replay has no slot to spend and the per-call reservation either overruns `MaxToolCalls` or double-charges.

## Risks / Trade-offs

- [A foreign backend cannot supply a call id, so its calls fail closed] → The only shipped foreign backend is eino, which supplies `ToolContext.CallID`; a backend without one fails loudly instead of running ungoverned.
- [Per-call gating adds a gate call per foreign tool call] → Foreign runtimes already schedule their own calls; the added gate is the same work the native path does per call.
- [Merging resolution could hide a genuine collision] → Keep the existing clash check and the duplicate-name failure; a collision still fails `Build`.
- [Deriving from the caller's context could inherit an already-cancelled context] → That is the contract: a cancelled caller cancels the run, matching native behaviour.
- [Moving `Grant`/`GrantStore` to `core/permission` touches `std/permission` and its callers] → The interface bodies are unchanged, so `MemoryGrantStore`, `ApproveGrant` and `HasGrant` keep their behavior and only name the relocated type; `testkit/conformance` already imports `std/permission` and needs no edit.
- [A stack that binds no grant store now refuses `ApproveScope` instead of approving silently] → That is the intended fail-closed contract, and `ApprovalGrants` is a `Build` option a caller supplies once; the refusal names `std/permission.ErrGrantRefused`.
- [Dropping the second recovery ledger changes what the recovery drive meters] → The dropped ledger was never the one the foreign tools charged; driving on the returned ledger makes cost and tool-call accounting agree between `Send`, `Resume` and `Recover`.
- [Seeding the recharge from the cumulative admitted count can over-reserve on a foreign replay] → The count is the run's own durable admitted total, and the replay spends exactly those slots, so the budget is charged once across both drives; the assertion in task 3.3 pins the charged total.

## Migration Plan

1. Land D4 first: registration merge is independent and unblocks mixed-stack builds.
2. Land D2 then D1: the governed path needs the call id to journal under a stable key.
3. Land D3 in the same wave as D1, since both touch the foreign run wiring.
4. Land D5 before D1's approval subtests run: `ApproveScope`, `ResumeInput.ScopeTTL`, `core/permission.Grant`/`GrantStore` and `ApprovalGrants` must be declared before the foreign resume can store a grant. D5 is declaration-only for `core`; the store implementation and its TTL policy stay in `std/permission`.
5. Land D6 with D1: the per-call reservation and the resume/recovery ledger fixes are one seam, because the reservation the foreign path charges is the ledger the recovery drive must drive on.
6. This change's `core/foreign_conversation.go` edit lands before `repair-conversation-lifecycle` edits the same file. The lifecycle edit follows and keeps both changes.
7. At apply time: register the 15 new scenario ids in `openspec/scenarios.json`, then edit the `Contract` blocks of `openspec/specs/runtime/spec.md`, `openspec/specs/permission/spec.md` and `openspec/specs/build/spec.md`. The driver regenerates the types index; no ADR number is reserved during authoring.
8. Rollback: each decision is behind its own seam; reverting the governed wrapper restores the previous behaviour without touching public signatures.

## Seal requirements

The apply-time implementer settles these before the package closes. Each item names the code the decision depends on.

1. Inspect `std.Shield` (`std/shield.go:15`) and `std.Journal` (`std/journal.go:70`) as they stand, then confirm the shared helper keeps them inside the composed tool chain and adds no second journal reservation around it.
2. Specify the persisted foreign approval continuation: which `runtime.State` field carries the suspended call, where the approval decision lands on resume, and how a resumed or recovered foreign drive converts an approved ask into a `runtime.Batch` run over one call. The `SuspendError{Payload}` field (`core/types/errors.go`) carries the pending call identity across the adapter interrupt (`adapter/eino/runtime.go:378`).
3. Map every task in `tasks.md` to a named observable transition assertion, or declare the task internal-only with its internal observable.
4. Record the legal public seeding paths for a foreign run's numeric budget: `WithLimits` for the flow limits (`core/limits.go:24`), the run's `Cost` seed the resumed and recovered drives pass (`core/foreign_conversation.go:71`), and the `MaxToolCalls` reservation one foreign call charges.
5. Both `openspec validate restore-runtime-governance --strict --no-interactive` and `openspec validate repair-conversation-lifecycle --strict --no-interactive` pass before the handoff is sealed.
6. Confirm the grant seam end to end: `core` and `core/permission` import no `std` package and declare no grant TTL default, eligibility table or grant audit kind; `std/permission` still owns `MaxGrantTTL`, `GrantedByScope`, `ErrGrantRefused`, `MemoryGrantStore`, `ApproveGrant` and `HasGrant`; the grant's `Subject` is the verifying approving subject and never the checkpoint's `Originator`.
7. Confirm one ledger per drive: `chains.LimitsStateFrom` inside a foreign tool call returns the same `*chains.LimitsState` the `Send`, `Resume` and `Recover` drive meters, and the resumed foreign drive's reservation count equals the run's cumulative admitted count.
