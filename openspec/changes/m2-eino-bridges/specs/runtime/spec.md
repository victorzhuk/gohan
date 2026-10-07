# Spec Delta

## MODIFIED Requirements

### Requirement: Component mixing

A backend MUST reach every governed model, tool and message conversion through the exported seams, so mixing a foreign component under the native runtime and mixing governed components inside a foreign graph run the same chains. A foreign tool bridge MUST present an imported framework tool as `types.Tool` with its declared `ToolSpec`, preserve argument, result and error fidelity, and route the call through `chains.ComposeToolChain`. A message bridge MUST declare its per-kind fidelity, MUST NOT drop a source block, and MUST preserve block order.

#### Scenario: foreign tool under native
ID: `runtime.foreign-tool-under-native`
- WHEN an imported eino `InvokableTool` runs under the native runtime
- THEN R2 "tool round trip" passes and the call goes through the full tool chain

#### Scenario: governed components in eino graph
ID: `runtime.governed-components-in-eino-graph`
- WHEN a graph is built with `einoflow.ModelNode` and `einoflow.ToolsNode`
- THEN R3 and R7 scenarios pass for that graph flow

#### Scenario: message round trip
ID: `runtime.message-round-trip`
- WHEN a `Message` with reasoning, text, image, document, tool use and `Raw` blocks is converted to eino `AgenticMessage` / adk-go `genai.Content` and back
- THEN block order is preserved and each block matches the adapter's declared fidelity (`Reasoning` preserved only through its own provider)
