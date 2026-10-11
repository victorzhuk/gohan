# Proposal: restore-runtime-governance

## Why

Foreign runs resolve through the native pipeline (`core/foreign_conversation.go:28-56`), but their tool calls do not reach the native effect boundary. `foreignTools` composes only the tool chain (`core/foreign_conversation.go:109-127`): `governedTool.Call` builds `types.ToolUse{Name, Args}` with an empty `ID` and calls `governedToolExec`, so no approval gate and no journal entry run. The native path gates and journals every call at the effect boundary (`openspec/specs/runtime/spec.md`, *Tool batch protocol*, rule 1; `permission` *Approval scope and requests*). A foreign run therefore executes side effects without approval and without a recoverable journal record.

Three further defects share the same seam. `foreignAgentRun` seeds its ledger on `context.Background()` (`core/foreign_conversation.go:72`), so `Send`, `Resume` and `Recover` lose caller identity, deadline and cancellation. The backend call id is dropped: eino hands it through `adk.ToolContext{Name, CallID}` at `WrapInvokableToolCall` (`adk/handler.go:44-47`), not through `tool.Option` (`components/tool/option.go:22`), and the bridge never reads it, so a recovered turn cannot match a completed call to its `CallKey`. `resolveForeign` resolves foreign definitions by calling `resolveNative` (`core/foreign_conversation.go:49`), which replaces the whole map (`core/native_resolve.go:173`), dropping every native flow's resolved configuration.

## What Changes

- Route every foreign tool call through the same per-call governed path the native batch protocol uses: approval gate, effect journal and cancel shield before the tool implementation.
- Carry a stable backend call id into the governed `ToolUse`, and fail closed with `Failed(Permanent)` before any side effect when the id is absent.
- Derive the foreign run's context from the caller's context, preserving principal, deadline and cancellation across `Send`, `Resume` and `Recover`; completed calls replay from the journal without re-execution.
- Resolve foreign definitions without replacing the native resolved configuration, keeping the duplicate-name failure.
- Honor a scoped approval on the foreign path: `Resume` stores the `ApproveScope` grant through the bound grant store before the approved call executes, keys the grant on the approving subject, and fails the resume closed when no store is bound. The grant vocabulary (`permission.Grant`, `permission.GrantStore`) lives in `core/permission`; the store implementation and its TTL policy stay in `std/permission`.
- Keep the resume and recovery drives on one ledger: the resumed foreign drive recharges the calls the first drive admitted, and `Recover` drives the foreign run on the ledger `foreignAgentRun` returns instead of wrapping a second one.
- **Not breaking:** `Send`'s signature and detached behaviour are unchanged; native resolution, chains and public entry paths are unchanged.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `runtime`: a foreign run uses the resolved native configuration and its tool calls pass the gate and journal per call, carry a stable call id, and receive the caller's context (`openspec/specs/runtime/spec.md`).
- `permission`: a foreign tool call reaches the gate on the same terms as a native call, including `Ask`, `Deny` and session grants; a scoped approval on the foreign path stores its grant through the bound grant store on the native terms (`openspec/specs/permission/spec.md`).
- `build`: foreign resolution preserves every native flow's resolved configuration and still fails closed on a duplicate name (`openspec/specs/build/spec.md`).

## Impact

The package touches `core/foreign_conversation.go` (the governed tool wrapper and the run context), `core/native_resolve.go` (registration merge), `core/native_effects.go` (the shared per-call governed path) and `adapter/eino/runtime.go` (reading the backend call id). It adds no capability and no new dependency. Imported-tool construction and message fidelity stay with `m2-eino-bridges`; lifecycle constructor common fields stay with `repair-conversation-lifecycle`.
