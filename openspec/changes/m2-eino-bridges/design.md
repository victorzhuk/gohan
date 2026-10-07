# Design: m2-eino-bridges

## 1. Context

The first wave shipped `adapter/eino` (archived `2026-10-07-m2-eino-runtime`). The table below separates what already ships from what this change adds, each bound to a symbol read this session.

| Symbol | Location | State |
|---|---|---|
| `EncodeMessages` / `DecodeMessages` | `adapter/eino/bridge.go:30,43` | ships — eino `types.Message` ↔ `*schema.AgenticMessage`; fidelity declared at `adapter/eino/bridge.go:12-18`; opaque kinds on the reserved `gohanBlockType` channel (`adapter/eino/bridge.go:19`) |
| `turnModel` | `adapter/eino/runtime.go:300` | ships — governed model bridge (`Generate` `:304`, `Stream` `:322`) |
| `turnTool` | `adapter/eino/runtime.go:360` | ships — governed gohan→eino tool bridge (`Info` `:365`, `InvokableRun` `:370`) |
| `nativeForeignTool` | `adapter/eino/foreign_tool_test.go:38` | **test-local only** — the sole eino-tool→`types.Tool` bridge; not exported |
| `ComposeToolChain` | `core/chains/chain.go:97` | ships — composes a chain over a `types.ToolFunc` (`:13`); `ToolChain` `:50` |
| `types.Tool` / `types.ToolSpec` | `core/types/tool.go:108,80` | ships — the port any imported tool must satisfy |
| `types.ToolUse` / `types.ToolResult` | `core/types/message.go:136,142` | ships — the call/result shapes |
| `conformance.Runtime` | `testkit/conformance/runtime.go:53` | ships — the acceptance suite both backends run |

Direction decides the work. `turnTool` (`adapter/eino/runtime.go:360`) bridges *governed gohan tools into eino* — the eino-graph direction. `runtime.foreign-tool-under-native` runs the opposite way: an *imported eino tool into the governed native batch*. No shipped symbol performs that direction; the test performs it with a local shim (`adapter/eino/foreign_tool_test.go:38`). This change exports that seam and proves its fidelity.

## 2. Goals / Non-Goals

**Goals**

- Ship the exported foreign-tool→native bridge and prove its fidelity through the governed chain.
- Give the message-fidelity contract a shared home both bridges run, and declare the M2/M3 split of `runtime.message-round-trip` explicitly.

**Non-Goals**

- Rebuilding the eino runtime, the model bridge or the message codec — all shipped.
- The eino graph direction (`einoflow.ModelNode` / `einoflow.ToolsNode`) — `m2-eino-graph-native-resume`.
- The adk-go bridge — M3 (`openspec/changes/m3/proposal.md:7`).

## 3. Decisions

**D1 — Export the tool bridge; the shim becomes the seam.** The bridge returns a value satisfying `types.Tool` (`core/types/tool.go:108`) over an imported eino `tool.BaseTool`/`tool.InvokableTool`, so the native runtime composes it exactly as it composes a native tool. The spec's *Component mixing* prose already requires the conversion go "through the exported seams" (`openspec/specs/runtime/spec.md:345`); a `_test.go` shim satisfies the chain but not the contract. *Alternative considered:* discover the bridge by type assertion behind an optional interface. Rejected — the port is `types.Tool` and the spec says reach the chain through the exported seam, so a second discovery path would add a seam the freeze then has to carry.

**D2 — Prove bridge fidelity, not the mixing chain again.** `m2-eino-runtime` already proved the chain runs (`adapter/eino/foreign_tool_test.go:167`). This change asserts that the bridge preserves what the port promises: the declared `ToolSpec` (name, description, effect) survives; arguments reach the tool as its own JSON; the tool result maps to `types.ToolResult` (`core/types/message.go:142`); an error classifies as the same value the chain would see; and the call passes every middleware step through `chains.ComposeToolChain` (`core/chains/chain.go:97`) with `ctx` unchanged.

**D3 — One message-fidelity contract, two legs.** The contract lives in `testkit/conformance` beside the suite at `testkit/conformance/runtime.go:53`, so the eino bridge runs it now and the adk-go bridge runs the identical assertions in M3. The eino bridge declares its fidelity at `adapter/eino/bridge.go:12-18`; the contract asserts block order, per-block-kind fidelity, no dropped source block, `Reasoning` only through its own provider, and opaque kinds through the reserved `gohanBlockType` channel (`adapter/eino/bridge.go:19`).

**D4 — `runtime.message-round-trip` is not claimed closed.** `openspec/changes/m2/design.md` §3 conflict 3 resolves the one id that names two bridges: "M2 proves the eino leg and records evidence for it; M3 proves the adk-go leg under the same id's requirement." This change is an M2 second-wave lane (`openspec/changes/m2/tasks.md` item 5) and ships no adk-go code, so it records the eino leg and defers the adk-go leg. *Recorded conflict:* the archived `m2-eino-runtime` package wrote that `m2-eino-bridges` owns the adk-go leg (its proposal *Impact* and its `design.md` §5). That contradicts §3 conflict 3 and `openspec/changes/m3/proposal.md:7`, which lists `adapter/adkgo` as M3 scope. No adk-go adapter `.go` file exists in M2. **Resolution adopted:** §3 conflict 3 governs; the adk-go leg is M3's and this change never claims the whole id closed.

The row `runtime.governed-components-in-eino-graph` is a third scenario of the same requirement, owned by `m2-eino-graph-native-resume` (`openspec/changes/m2/design.md` §2.1). It is reproduced in the delta only because a MODIFIED requirement carries the complete block; no task here binds it.

## 4. Contract surface

The delta edits requirement prose only, inside `## Requirements` of `openspec/specs/runtime/spec.md` (the *Component mixing* block at `:343`). It edits no Contract block and no types index. The three scenario blocks (their `ID:` lines and WHEN/THEN lines) are reproduced verbatim so the registry keeps matching them; the added normative text lands in the requirement description only.

## 5. Risks / Trade-offs

- [A second tool-discovery path] → the bridge returns `types.Tool`; no type assertion, no optional interface, no new port method.
- [The adk-go leg is re-claimed by an M2 lane] → the delta and the tasks bind the eino leg only; a task records the deferral and a grep keeps `adkgo` out of `testkit/conformance` tests.
- [The shared message contract drifts from the codec] → the contract asserts directly against `EncodeMessages`/`DecodeMessages` (`adapter/eino/bridge.go:30,43`) and the eino leg re-runs the shipped `TestMessageBridge/runtime.message-round-trip`.
- [The exported seam outgrows the freeze] → the bridge adds no `runtime` port member; `m2-api-freeze` freezes only the method sets the archived first-wave design settled.

## 6. Evidence this change owes

- `runtime.foreign-tool-under-native` — the exported-bridge fidelity subtests in `adapter/eino`, extending the mixing test at `adapter/eino/foreign_tool_test.go:167`. The chain leg stays `m2-eino-runtime`'s.
- `runtime.message-round-trip` — the eino leg, proved through the shared contract in `testkit/conformance` and the shipped `TestMessageBridge/runtime.message-round-trip`. The adk-go leg is deferred to M3 and is not claimed.
- Integration: `task lint`, `go -C adapter/eino test -short -timeout 2m ./...`, `go test -short -timeout 2m ./testkit/...` and `task spec:coverage` green, with the two bound ids covered and no new `missing_in_scope`.
