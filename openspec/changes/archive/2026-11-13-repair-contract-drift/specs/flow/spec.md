# flow delta

## ADDED Requirements

### Requirement: Native definition construction

A native flow MUST be registered as a `gohan.NativeSpec` through `gohan.WithNativeAgent` before `Build` resolves it, and `gohan.NewNativeConversation(stack, name, opts...)` MUST read that resolution without recomputing it. `Request.Tools` MUST derive from `NativeSpec.Tools` during resolution and MUST NOT be supplied independently. An unknown definition or profile name MUST fail before any provider call.

#### Scenario: definition resolved once
ID: `flow.native-definition-resolved-once`
- WHEN a stack registers one `NativeSpec` and `Build` resolves its profile, chains and tools
- THEN `NewNativeConversation` reads that resolution, and `Explain` projects the same configuration with zero provider calls and no run budget spent

#### Scenario: tools have one source
ID: `flow.native-tools-single-source`
- WHEN a `NativeSpec` declares `Tools` and its `Request` carries a different tool list
- THEN resolution derives `Request.Tools` from `NativeSpec.Tools`, and the resolved flow executes exactly the derived set