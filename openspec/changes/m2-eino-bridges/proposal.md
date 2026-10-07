# Proposal

## Why

M2's first wave shipped the eino runtime and the raw model, tool and message bridges (`m2-eino-runtime`, archived `2026-10-07`). Two `runtime` acceptance rows still sit unproven on the bridge-fidelity side, both assigned to `m2-eino-bridges` by `openspec/changes/m2/design.md` §2.1.

`runtime.foreign-tool-under-native` (row 28): the mixing chain runs, but no shipped adapter symbol exposes a supported foreign-tool-to-native seam. The only bridge from an imported eino `tool.InvokableTool` to `types.Tool` lives in a test file — `nativeForeignTool` (`adapter/eino/foreign_tool_test.go:38`), used by `TestForeignToolUnderNative` (`adapter/eino/foreign_tool_test.go:167`). The *Component mixing* requirement already demands the conversion go "through the exported seams" (`openspec/specs/runtime/spec.md:345`); a test-local shim satisfies the chain but not that contract.

`runtime.message-round-trip` (row 30): the eino leg ships — `EncodeMessages`/`DecodeMessages` (`adapter/eino/bridge.go:30,43`) with its declared fidelity (`adapter/eino/bridge.go:12-18`) and the reserved opaque channel `gohanBlockType` (`adapter/eino/bridge.go:19`), proven by `TestMessageBridge/runtime.message-round-trip` (`adapter/eino/message_bridge_test.go`). The fidelity contract has no shared home both bridges run, and the adk-go leg does not exist.

Why now: M2 is the freeze point (`docs/overview.md:110-112`), so the exported seam this change adds must settle before `m2-api-freeze`, and the M2/M3 split of the message id must be stated while the lane is still open.

## What Changes

- **Added** an exported foreign-tool bridge in `adapter/eino`: it presents an imported eino `tool.BaseTool`/`tool.InvokableTool` as `types.Tool` with the tool's declared `ToolSpec`, preserves argument, result and error fidelity, and routes the call through `chains.ComposeToolChain` (`core/chains/chain.go:97`) like any governed tool. What exists today is only the test-local `nativeForeignTool` (`adapter/eino/foreign_tool_test.go:38`); the exported symbol does not exist.
- **Added** a shared message-fidelity contract in `testkit/conformance` beside the suite at `testkit/conformance/runtime.go:53`, so the eino bridge satisfies it now and the adk-go bridge satisfies the identical contract in M3. It asserts block order, per-block-kind fidelity, no dropped source block, `Reasoning` only through its own provider, and opaque kinds through the reserved channel.
- **Modified** the `runtime` *Component mixing* requirement: the prose names the tool-bridge fidelity obligation and the message-bridge declared-fidelity obligation; the three scenario blocks are reproduced byte-identically.

The model and tool bridges' turn machinery (`turnModel` `adapter/eino/runtime.go:300`, `turnTool` `adapter/eino/runtime.go:360`) shipped with the first wave and are not rebuilt here.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `runtime`: one requirement modified — *Component mixing* — carrying the `runtime.foreign-tool-under-native` and `runtime.message-round-trip` scenarios.

### Scenarios bound by this change

Two ids, both `runtime`:

- `runtime.foreign-tool-under-native` (row 28) — this change proves the **bridge-fidelity** leg only. The mixing chain (an imported eino tool running the full governed batch under the native runtime) was proven by `m2-eino-runtime` (`adapter/eino/foreign_tool_test.go:167`); this change adds the exported seam and its fidelity assertions.
- `runtime.message-round-trip` (row 30) — the milestone splits one id across bridges. **Resolution adopted:** M2 proves the eino leg and records it; M3 proves the adk-go leg under the same requirement. This change does **not** claim the id closed on one bridge. Recorded conflict: the archived `m2-eino-runtime` package wrote that `m2-eino-bridges` "owns the adk-go leg" (its proposal *Impact*; its `design.md` §5), while `openspec/changes/m2/design.md` §3 conflict 3 says "M3 proves the adk-go leg under the same id's requirement", `openspec/changes/m3/proposal.md:7` lists `adapter/adkgo` as M3 scope, and `openspec/changes/m2/tasks.md` item 5 lists this change as an M2 second-wave lane. No adk-go adapter code exists in M2 (a `grep` for `adkgo` finds no `.go` adapter), so the adk-go leg cannot land in this lane; §3 conflict 3 governs and the adk-go leg is M3's.

`runtime.governed-components-in-eino-graph` is a third scenario in the same requirement but is not bound here: `m2/design.md` §2.1 assigns its acceptance row to `m2-eino-graph-native-resume`, so it is reproduced in the delta only to keep the requirement block complete.

## Impact

- Code: `adapter/eino` gains an exported foreign-tool bridge; `testkit/conformance` gains a message-fidelity contract both backends' bridges run.
- Contract: `runtime.Runtime`, `runtime.AgentRun` and `StepGranularity` are untouched — this change adds no port method and no `runtime` field. No Contract block and no types index is edited.
- Dependency: consumes `m2-eino-runtime` (archived) and the shipped governed tool chain; leaves the adk-go leg to M3; `m2-api-freeze` freezes the seam this change exports.
- Not in scope: the eino graph flow and Native resume (`m2-eino-graph-native-resume`); the adk-go bridge (M3); any change to the shipped model/tool/message bridge machinery.
