# Spec Delta

## MODIFIED Requirements

### Requirement: Component mixing

A backend MUST reach every governed model, tool and message conversion through the exported seams, so mixing a foreign component under the native runtime and mixing governed components inside a foreign graph run the same chains. A message bridge MUST declare the fidelity it keeps, and MUST NOT drop a block the source carried.

A foreign tool bridge MUST validate its declared spec before execution and MUST NOT infer a governance field the upstream tool info omits. A message bridge MUST carry each block's `Origin` and `Seq` and any blob the source held.

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

## ADDED Requirements

### Requirement: Foreign tool governance

A foreign tool bridge MUST present an imported framework tool as `types.Tool` and MUST take its governance metadata explicitly. It MUST NOT infer `Effect`, `Risk` or `Capabilities` from a field the upstream tool info omits. A zero `Trust` MUST resolve to `Untrusted`. The bridge MUST validate the declared spec before the tool can execute, MUST preserve argument, result and error identity so `errors.Is` holds, and MUST route every call through the governed tool chain.

#### Scenario: foreign tool spec declared
ID: `runtime.foreign-tool-spec-declared` (proposed)
- WHEN a bridge is constructed with an explicit `types.ToolSpec`
- THEN the returned value reports that spec and a mis-declared upstream tool info fails construction before any call runs

#### Scenario: foreign tool trust untrusted
ID: `runtime.foreign-tool-trust-untrusted` (proposed)
- WHEN a bridge is constructed with the zero `Trust` value
- THEN the reported spec carries `types.Untrusted`, whatever the upstream tool info declares

#### Scenario: foreign tool error identity
ID: `runtime.foreign-tool-error-identity` (proposed)
- WHEN an imported tool returns a wrapped sentinel error
- THEN `errors.Is` holds for that sentinel at the caller of the governed chain

### Requirement: Foreign tool result mapping

A foreign tool bridge MUST take its result mode as an explicit construction choice. The default constructor MUST carry every result string verbatim as one `Text` block and MUST NOT parse it. The envelope constructor MUST parse only a JSON object whose single key is `gohan_tool_result` and whose value is a `types.ToolResult` with content blocks or an explicit outcome. It MUST keep every other string as text and MUST NOT fail a call on it. No argument schema selects the mode.

#### Scenario: foreign tool result mapped
ID: `runtime.foreign-tool-result-mapped` (proposed)
- WHEN the default constructor is used and the imported tool returns any string
- THEN the result holds one `Text` block with that string verbatim, and the string is never parsed as JSON

#### Scenario: foreign tool envelope result parsed
ID: `runtime.foreign-tool-envelope-parsed` (proposed)
- WHEN the envelope constructor is used and the imported tool returns a JSON object whose only key is `gohan_tool_result`
- THEN the envelope value is parsed into the reported `ToolResult` blocks, and any other string stays one `Text` block

### Requirement: Message bridge fidelity

A message bridge MUST carry each block's `Origin` and `Seq` and any blob the source held, whether stored as `Data`/`URL` or as `Blob`, for every block kind it declares. It MUST NOT drop a URL that accompanies data. A block kind the framework carries without a channel of its own MUST round-trip through the reserved opaque channel.

#### Scenario: message block base carried
ID: `runtime.message-block-base-carried` (proposed)
- WHEN a message carries `Origin` and `Seq` on every block kind the adapter declares
- THEN the converted and re-decoded blocks keep the same origin and sequence

#### Scenario: message blob carried
ID: `runtime.message-blob-carried` (proposed)
- WHEN an `Image`, `Audio` or `File` block carries `Data`, a `URL` and a `Blob`
- THEN all three survive the conversion and no `URL` is discarded