# Proposal

## Why

M2's first wave shipped the eino runtime and the raw model, tool and message bridges (`m2-eino-runtime`, archived `2026-10-07`). Three acceptance rows sit unproven on the bridge-fidelity side, and all three are assigned to `m2-eino-bridges`: two by `openspec/changes/m2/design.md` §2.1 and one by §2.5.

`runtime.foreign-tool-under-native` (row 28): the mixing chain runs, but no shipped adapter symbol exposes a supported foreign-tool-to-native seam. The only bridge from an imported eino `tool.InvokableTool` to `types.Tool` lives in a test file — `nativeForeignTool` (`adapter/eino/foreign_tool_test.go:38`), whose `Spec()` returns only `types.ToolSpec{Name: "echo"}` (`:40`) and whose `Call` unmarshals the upstream string straight into `types.ToolResult` (`:44`). The proof harness calls that shim directly instead of the governed pipeline, so nothing reaches `Build`/`NewNativeConversation`/`Send`. The *Component mixing* requirement demands every conversion go through the exported seams (`openspec/specs/runtime/spec.md:343`); a test-local shim satisfies the chain but not the contract.

`runtime.message-round-trip` (row 30): the eino leg ships — `EncodeMessages`/`DecodeMessages` (`adapter/eino/bridge.go`) — but the codec drops the `BlockBase` (`Origin`, `Seq`) of every natively-carried kind and ignores the `Blob` field on `Image`/`Audio`/`File` (`core/types/message.go:57,74`); `blobFields` keeps only `Data` or `URL`. The declared fidelity (`adapter/eino/bridge.go:12-19`) names block order and no dropped block, so the codec must carry the block base and the blob too. The adk-go leg does not exist.

`guards.origin-survives-conversion` (`openspec/specs/guards/spec.md:117`): `BlockOrigin()` must survive a tool-result `Part`'s conversion to the framework types and back. No test names it today, and the eino codec above is exactly where it must be proven.

Why now: M2 is the freeze point, so the exported seam and the metadata contract it carries must settle before `m2-api-freeze`.

## What Changes

- **Added** an exported foreign-tool bridge in `adapter/eino`. It presents an imported eino `tool.InvokableTool` as `types.Tool` (`core/types/tool.go:108`). It exports two constructors with these exact signatures, with `tool.InvokableTool` and `types.ToolSpec` from the sources read for this change. The second constructor selects the declared result mode; nothing else selects it:

  ```go
  func NewForeignTool(ctx context.Context, imported tool.InvokableTool, spec types.ToolSpec) (types.Tool, error)
  func NewForeignToolWithEnvelope(ctx context.Context, imported tool.InvokableTool, spec types.ToolSpec) (types.Tool, error)
  ```

  `spec` carries `Effect`, `Risk`, `Trust`, `RequiredScopes`, `Timeout` and `Egress` (`core/types/tool.go:80-101`). The constructor calls `Info(ctx)` once, validates the returned `*schema.ToolInfo`, resolves a zero `Trust` to `types.Untrusted`, and never reads `Effect`, `Risk` or `Capabilities` out of the upstream info. Result mapping is a declared bridge rule, never a guess. Upstream `tool.InvokableTool.InvokableRun` returns a plain string and declares no result discriminator (`components/tool/interface.go:38-46`), so the bridge owns the mode. `NewForeignTool` uses the conservative default and carries the upstream string verbatim as one `Text` block. `NewForeignToolWithEnvelope` recognizes exactly one shape: a JSON object with the single key `gohan_tool_result`, whose value is a `types.ToolResult` JSON document. The bridge parses that shape and keeps every other string as text. No other key, no nested path and no field order selects a mode. An error the imported tool returns surfaces unchanged, so `errors.Is` holds across the bridge.
- **Fixed** the message codec in `adapter/eino`: it carries `BlockBase.Origin`/`Seq` for every declared block kind and the `Blob` (`Ref`, `SHA256`, `Bytes`) of `Image`/`Audio`/`File`, so `TestMessageBridge` (`adapter/eino/message_bridge_test.go:12`) keeps full fidelity under `reflect.DeepEqual`.
- **Added** a message-fidelity contract in `testkit/conformance` beside the suite (`testkit/conformance/runtime.go:53`), satisfied by the eino codec now and by the adk-go bridge in M3.
- **Proved** `runtime.foreign-tool-under-native` through the public path: a stack built with `Build` (`core/build.go:147`), a conversation from `NewNativeConversation` (`core/native_conversation.go:19`), driven by `Send`, with the exported bridge registered as a tool; an allowed call runs through the governed chain and a denied call is blocked by the batch gate. The direct-shim harness is replaced.
- **Modified** the `runtime` *Component mixing* requirement: its prose states only that a foreign tool bridge MUST validate its declared spec before execution, MUST NOT infer an omitted governance field, and that a message bridge MUST carry `Origin`, `Seq` and any blob. The three existing scenario blocks are reproduced byte-identically.
- **Added** three focused `runtime` requirements — *Foreign tool governance*, *Foreign tool result mapping* and *Message bridge fidelity* — with seven new proposed scenario ids. The governance detail lives here instead of in *Component mixing*, so this change adds a focused requirement rather than a competing replacement of the broad one.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `runtime`: one requirement modified — *Component mixing* — carrying the `runtime.foreign-tool-under-native`, `runtime.governed-components-in-eino-graph` and `runtime.message-round-trip` scenarios.

### Scenarios bound by this change

Three existing ids: two `runtime` and one `guards`.

- `runtime.foreign-tool-under-native` (row 28) — the bridge-fidelity leg, proved through the public `Build`/`NewNativeConversation`/`Send` path. The mixing chain leg (`m2-eino-runtime`) stays.
- `runtime.message-round-trip` (row 30) — the **eino leg** only. This change extends the archived eino acceptance of that row (`openspec/changes/archive/2026-10-07-m2-eino-runtime/specs/runtime/spec.md:170`); it adds no duplicate row and does not add a new id for the same claim. M3 proves the adk-go leg under the same requirement, so the id stays open.
- `guards.origin-survives-conversion` (`openspec/specs/guards/spec.md:117`) — the eino codec preserves `BlockOrigin()` across tool-result conversion; the adk-go leg is M3's.

Seven new ids are proposed in the three added requirements: `runtime.foreign-tool-spec-declared`, `runtime.foreign-tool-trust-untrusted`, `runtime.foreign-tool-result-mapped`, `runtime.foreign-tool-envelope-parsed`, `runtime.foreign-tool-error-identity`, `runtime.message-block-base-carried`, `runtime.message-blob-carried`.

`runtime.governed-components-in-eino-graph` is a third scenario of the same modified requirement, owned by `m2-eino-graph-native-resume` (`m2/design.md` §2.1); it is reproduced in the delta only because a MODIFIED requirement carries the complete block, and no task here binds it.

## Impact

- Code: `adapter/eino` gains `NewForeignTool` and codec fidelity fixes; `testkit/conformance` gains a message-fidelity contract.
- Contract: `runtime.Runtime`, `runtime.AgentRun` and `StepGranularity` are untouched — no port method, no `runtime` field, no Contract block.
- Registry: `openspec/scenarios.json` gains the seven proposed ids at apply time. The three existing ids keep their entries; no row is duplicated.
- Types index: `docs/design/types.md` is regenerated at apply time with `task spec:types`, because the delta adds `### Requirement:` blocks whose prose names identifiers the generator scans. The regeneration is declared in the design (§4) and in task 5.3; authoring does not touch the generated file.
- Dependency: consumes `m2-eino-runtime` (archived) and the shipped governed tool chain; leaves the adk-go leg to M3; `m2-api-freeze` freezes the seam this change exports. Ordering: the runtime composition lane (`restore-runtime-governance`) lands first, because both edit `adapter/eino/runtime.go` and the governed call path.
- Not in scope: the eino graph flow and Native resume (`m2-eino-graph-native-resume`); the adk-go bridge (M3); the backend call-id binding (`restore-runtime-governance`); foreign runtime execution governance.