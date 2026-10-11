# Design: m2-eino-bridges

## 1. Context

The first wave shipped `adapter/eino` (archived `2026-10-07-m2-eino-runtime`). The table separates what already ships from what this change adds, each bound to a symbol read for this package.

| Symbol | Location | State |
|---|---|---|
| `EncodeMessages` / `DecodeMessages` | `adapter/eino/bridge.go` | ships — eino `types.Message` ↔ `*schema.AgenticMessage`; fidelity declared at `adapter/eino/bridge.go:12-19`; opaque kinds on the reserved `gohanBlockType` channel (`adapter/eino/bridge.go:19`) |
| `encodeBlock` / `decodeBlock` | `adapter/eino/bridge.go` | ships — but drops `BlockBase.Origin`/`Seq` for `Text`, `Reasoning`, `Image`, `Audio`, `File`, `ToolUse`, and ignores `Blob` on `Image`/`Audio`/`File` |
| `blobFields` | `adapter/eino/bridge.go` | ships — keeps `Data` **or** `URL` only |
| `nativeForeignTool` | `adapter/eino/foreign_tool_test.go:38` | **test-local only** — sole eino-tool→`types.Tool` bridge; `Spec()` returns `types.ToolSpec{Name:"echo"}` (`:40`); not exported |
| `nativeMixHarness.batchEffect` | `adapter/eino/foreign_tool_test.go` | **test-local only** — calls the shim directly, bypassing the governed pipeline |
| `tool.InvokableTool` | `github.com/cloudwego/eino@v0.9.1/components/tool/interface.go:42` | upstream port — embeds `BaseTool` (`:32`) and adds `InvokableRun(ctx, argumentsInJSON string, opts ...Option) (string, error)` |
| `schema.ToolInfo` | `github.com/cloudwego/eino@v0.9.1/schema/tool.go:128` | upstream metadata — `Name`, `Desc`, `Extra`, `ParamsOneOf`; it carries no `Effect`, `Risk`, `Trust` or egress field |
| `types.Tool` / `types.ToolSpec` | `core/types/tool.go:108` / `:80` | ships — the port and the full declaration; `ToolSpec.Trust` is `Trusted` for own code |
| `chains.ComposeToolChain` | `core/chains/chain.go:97` | ships — composes the chain over a `types.ToolFunc`; `RunToolChain` (`:128`) uses the zero call, so a real call MUST go through `ComposeToolChain` |
| `Build` | `core/build.go:147` | ships — resolves `NativeSpec.Tools` into the run |
| `NewNativeConversation` | `core/native_conversation.go:19` | ships — the streaming public entry; `Send` drives it |
| `types.BlockBase` / `Blob` / `ToolResult` | `core/types/message.go:57,74,142` | ships — `Origin`, `Seq`; blob `Ref`, `SHA256`, `Bytes`; tool results embed `BlockBase`, so `BlockOrigin()` reaches every one of them |
| `conformance.Runtime` / `Harness` | `testkit/conformance/runtime.go:53,15` | ships — the acceptance suite both backends run |

Direction decides the work. `turnTool` bridges *governed gohan tools into eino* — the eino-graph direction. `runtime.foreign-tool-under-native` runs the opposite way: an *imported eino tool into the governed native batch*. No shipped symbol performs that direction; only the test shim does, and it does so outside the public path. This change exports the seam, makes it carry declared governance metadata, fixes the codec fidelity, and proves the behavior through `Build`/`NewNativeConversation`/`Send`.

## 2. Goals / Non-Goals

**Goals**

- Ship the exported foreign-tool→native bridge whose construction takes explicit, validated governance metadata, and prove it (allowed and denied) through the public path.
- Fix the message codec so block origin, sequence and blob survive conversion for every declared kind.
- Give the message-fidelity contract a shared home and record the eino leg of `runtime.message-round-trip`.

**Non-Goals**

- Rebuilding the eino runtime, the model bridge or the message codec's block-kind coverage — all shipped.
- The eino graph direction (`einoflow.ModelNode` / `einoflow.ToolsNode`) — `m2-eino-graph-native-resume`.
- The backend call-id binding and the shared per-call governed path — `restore-runtime-governance`, which edits the same driver files and lands first.
- The adk-go bridge and the adk-go leg of both `runtime.message-round-trip` and `guards.origin-survives-conversion` — M3 (`openspec/changes/m3/proposal.md:7`).

## 3. Decisions

**D1 — Export the tool bridge; the shim becomes the seam.** The bridge returns a value satisfying `types.Tool` (`core/types/tool.go:108`) over an imported `tool.InvokableTool`, so the native runtime composes it exactly as it composes a native tool. The spec's *Component mixing* prose requires the conversion go "through the exported seams" (`openspec/specs/runtime/spec.md:343`); a `_test.go` shim satisfies the chain but not the contract. *Alternative considered:* discover the bridge by type assertion behind an optional interface. Rejected — the port is `types.Tool`; a second discovery path is a seam the freeze would have to carry.

**D2 — One constructor, explicit `types.ToolSpec`, validated before execution.** The exported signature is fixed:

```go
func NewForeignTool(ctx context.Context, imported tool.InvokableTool, spec types.ToolSpec) (types.Tool, error)
```

The full `types.ToolSpec` is the argument rather than a smaller metadata struct, because the field set is already the port's own declaration (`core/types/tool.go:80-101`) and a parallel struct would be a second spelling of the same facts. The `ctx` parameter is the construction context the constructor spends on its one `Info(ctx)` call; it is not retained and not used per call. The returned value's `Spec()` reports that declared spec with the trust resolution applied. The constructor calls `Info(ctx)` once and fails on an error, a nil info, an empty name, or a name that disagrees with `spec.Name`, so a mis-declared bridge never reaches the governed batch. It MUST NOT read `Effect`, `Risk`, `Capabilities` or egress out of the upstream info — `schema.ToolInfo` carries none of them (`schema/tool.go:128-143`), and inventing a zero would classify a side-effecting tool as read-only and bypass the gate. A zero `Trust` resolves to `types.Untrusted`, because the tool is imported. *Alternative considered:* accept `[]types.ToolOption`. Rejected — options mutate the declared spec after the caller stated it, so the validated result would not be the spec the caller read.

**D3 — Result mapping is a declared bridge mode, not a guess and not the argument schema.** Upstream `tool.InvokableTool.InvokableRun` returns a plain string and says nothing about the shape of that string (`components/tool/interface.go:38-46`), and `schema.ToolInfo` carries no result field (`schema/tool.go:128-141`). The mapping is therefore a property of the bridge, and the caller selects it at construction:

```go
func NewForeignTool(ctx context.Context, imported tool.InvokableTool, spec types.ToolSpec) (types.Tool, error)
func NewForeignToolWithEnvelope(ctx context.Context, imported tool.InvokableTool, spec types.ToolSpec) (types.Tool, error)
```

- `NewForeignTool` is the conservative default. Every upstream string becomes one `Text` block, byte for byte. No JSON parsing happens on this path.
- `NewForeignToolWithEnvelope` recognizes exactly one envelope: a JSON object with the single key `gohan_tool_result`, whose value is a `types.ToolResult` JSON document. Recognition requires all of: the string is a JSON object, the object has that key, the key value is non-empty, and the value unmarshals into a `types.ToolResult` that carries at least one content block or an explicit outcome. A string that fails any of those checks stays text. The bridge never fails a call because a string is not an envelope.

The mode never depends on `spec.Schema`, on the upstream info, or on the result content. `spec.Schema` describes the tool's arguments, not its result, so an argument schema does not license structured output and its absence does not forbid it. `spec.ToolResult` shape and key name are bridge-owned and identical in the proposal, this decision, the delta scenario and task 1.5. No retry, no fallback framework, no second discriminator, no field-order sensitivity. An error the imported tool returns surfaces unchanged, so `errors.Is` holds across the bridge.

*Alternative rejected:* parse a `types.ToolResult` whenever the string happens to unmarshal into one. Rejected — that makes ordinary JSON tool output flip between two meanings without any declaration, and no argument or upstream field can rule it out. *Alternative rejected:* treat `EnhancedInvokableTool` (`components/tool/interface.go:59-69`) as the structured path. Rejected — it is a different port with a different constructor, so supporting it is a separate seam and a separate requirement.

**D4 — Prove through the public path, not the shim.** The acceptance test builds a stack with `Build` (`core/build.go:147`) registering a `NativeSpec` whose `Tools` hold the exported bridge, opens the conversation with `NewNativeConversation` (`core/native_conversation.go:19`), and drives `Send`. Two legs: an **allowed** imported tool call passes the gate, runs the tool through the composed chain, and its result reaches the next model turn; a **denied** imported tool call is refused by the batch gate and gets a `Failed(Permanent)` result, and the imported tool is never invoked. The test-local `nativeMixHarness`/`nativeForeignTool` direct-shim path is removed. The mixing-chain leg stays `m2-eino-runtime`'s; this change adds the exported seam and the public-path fidelity proof.

**D5 — The codec carries block base and blob.** `encodeBlock`/`decodeBlock` (`adapter/eino/bridge.go`) MUST carry `BlockBase.Origin` and `BlockBase.Seq` for every kind the fidelity comment declares — `Text`, `Reasoning`, `Image`, `Audio`, `File`, `ToolUse` — not only the opaque kinds, which already round-trip as whole values. The carrier is the per-block `schema.ContentBlock.Extra` map, which already carries the opaque kind's payload (`adapter/eino/bridge.go:19`): two reserved keys hold the origin and the sequence, and the decoder removes them before it reads the native channel fields, so a decoded block carries no reserved key. `Image`/`Audio`/`File` MUST carry `Blob` (`Ref`, `SHA256`, `Bytes`) through the same reserved map beside `Data`/`URL`, and `blobFields` MUST NOT discard a URL that accompanies data. `TestMessageBridge` (`adapter/eino/message_bridge_test.go:12`) extends its fixture with `Origin`, `Seq` and `Blob` and keeps the `reflect.DeepEqual(decoded, in)` assertion. The deliberate drop of foreign-provider `Reasoning` stays as declared (`adapter/eino/bridge.go:12-19`); it is fidelity, not a contradiction.

**D6 — One message-fidelity contract, two legs; origin proven where conversion happens.** The contract lives in `testkit/conformance` beside the suite (`testkit/conformance/runtime.go:53`), taking the two codec functions as arguments, so the eino bridge satisfies it now and the adk-go bridge satisfies the identical assertions in M3. It names no framework type, because `testkit` does not depend on `adapter/eino` (`adapter/eino` depends on `testkit`, not the reverse). Its self-check against a fixture codec proves only the contract; the acceptance evidence is the real eino codec satisfying it in `adapter/eino`. `guards.origin-survives-conversion` (`openspec/specs/guards/spec.md:117`) is proven in `adapter/eino`, beside the codec that performs the conversion. The `guards` requirement's normative text is unchanged, so no `guards` delta is written, and no new scenario id duplicates that row: the assertion runs as a subtest of the existing `TestMessageBridge` tree.

**D7 — `runtime.message-round-trip` extends the archived eino leg; it is not a new row.** `m2/design.md` §3 conflict 3 resolves the one id that names two bridges. The archived first wave already bound that id to `TestMessageBridge/runtime.message-round-trip` for the eino leg (`openspec/changes/archive/2026-10-07-m2-eino-runtime/specs/runtime/spec.md:170`). This change reuses that exact subtest name and extends its fixture with `Origin`, `Seq` and `Blob`; it adds no second scenario block, no second registry row and no duplicate coverage claim. The row stays open until M3 lands the adk-go leg under the same id's requirement. The row `runtime.governed-components-in-eino-graph` is a third scenario of the same requirement, owned by `m2-eino-graph-native-resume`; it is reproduced in the delta only because a MODIFIED requirement carries the complete block, and no task here binds it.

**D8 — Governance detail goes into three new requirements, not into *Component mixing*.** The broad *Component mixing* requirement keeps a two-sentence clarification only. The normative detail — validation, the `Untrusted` default, the refusal to infer an omitted field, argument/result/error identity, the chain route — lands in the new *Foreign tool governance* requirement. The declared result mode (D3) lands in its own *Foreign tool result mapping* requirement, because it states one behavior and keeps that block under the 500-character limit. The block-base and blob obligation lands in the new *Message bridge fidelity* requirement. This keeps one owner per obligation, so no lane's edits can contradict another lane's restatement of a broad requirement. Seven new scenario ids are proposed and unused by another package.

## 4. Contract surface

The delta edits `openspec/specs/runtime/spec.md` in two ways. It MODIFIES the *Component mixing* requirement, inside `## Requirements` (the block at `:343`), adding two sentences to the description and reproducing all three existing scenario blocks byte-identically. It ADDS three requirements, *Foreign tool governance*, *Foreign tool result mapping* and *Message bridge fidelity*, with three, two and two proposed scenarios respectively. The result mapping is a separate requirement because it states one behavior — how a string becomes a result — and each requirement stays under the 500-character limit (D8).

It edits no Contract block and adds no Go code block, so no port, method or struct enters a main spec.

The types index is regenerated at apply time. `tools/gen_types_index.py` scans `openspec/specs/*/spec.md` for Go code blocks and derives `docs/design/types.md`; this delta adds no code block, so the regenerated file is expected to be byte-identical unless a concurrent change lands one. Task 5.3 runs `task spec:types` and the file must show no unaccounted drift. The proposal's *Impact* section, this section and task 5.3 declare the same step in the same terms.

No ADR number is reserved during authoring. The exported bridge is a new `adapter/eino` symbol, not a change to a frozen port method set, so no Contract edit follows from it.

## 5. Risks / Trade-offs

- [A second tool-discovery path] → the bridge returns `types.Tool`; no type assertion, no optional interface, no new port method.
- [A zero `Effect`/`Trust` on the imported spec] → the constructor validates the spec and resolves a zero `Trust` to `Untrusted`; no field is read from the upstream `Info` (D2).
- [The codec's fidelity claim outruns its fixture] → `TestMessageBridge` asserts `reflect.DeepEqual` over a fixture carrying `Origin`, `Seq` and `Blob` (D5).
- [Reserved keys leak into a decoded block] → the decoder strips them before reading the native fields, and the `reflect.DeepEqual` fixture check fails on any leftover key (D5).
- [The contract imports the eino module into `testkit`] → the contract takes the two codec functions as values and names no framework type (D6).
- [The adk-go leg is re-claimed by an M2 lane] → the delta and the tasks bind the eino leg only; a task records the deferral and a grep keeps `adkgo` out of `testkit/conformance` tests.
- [Two lanes edit `adapter/eino/runtime.go`] → `restore-runtime-governance` lands first; this change touches `adapter/eino/bridge.go` and a new `tool_bridge.go` only (D2, §2).
- [The exported seam outgrows the freeze] → the bridge adds no `runtime` port member; `m2-api-freeze` freezes only the method sets the archived first-wave design settled.
- [Ordinary JSON tool output is parsed as a tool result] → the default constructor never parses, and the envelope constructor recognizes one declared key (`gohan_tool_result`) that upstream never emits (`components/tool/interface.go:38-46`), so no string changes meaning by accident (D3).

## 6. Evidence this change owes

- `runtime.foreign-tool-under-native` — an allowed and a denied imported tool driven through `Build`/`NewNativeConversation`/`Send` in `adapter/eino`, with the exported bridge registered as the `NativeSpec` tool. The direct-shim harness is gone. The chain leg stays `m2-eino-runtime`'s.
- `runtime.message-round-trip` — the eino leg, extended from the archived subtest: `TestMessageBridge/runtime.message-round-trip` (`adapter/eino/message_bridge_test.go:13`) now carries `Origin`, `Seq` and `Blob`, and the shared contract in `testkit/conformance` runs against the real eino codec. The adk-go leg is deferred to M3 and is not claimed.
- `guards.origin-survives-conversion` — the `BlockOrigin()` round-trip assertion inside the `TestMessageBridge` tree in `adapter/eino`, beside `encodeBlock`/`decodeBlock`.
- New proposed ids — one named subtest each, listed in task group 1 and group 2. `runtime.foreign-tool-result-mapped` covers the default text mapping; `runtime.foreign-tool-envelope-parsed` covers the declared envelope.
- Integration: `task lint`, `go -C adapter/eino test -short -timeout 2m ./...`, `go test -short -timeout 2m ./testkit/...` and `task spec:coverage` green, with the bound ids covered and no new `missing_in_scope`.