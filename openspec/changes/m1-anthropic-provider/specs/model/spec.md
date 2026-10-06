# Spec Delta

## ADDED Requirements

### Requirement: Anthropic adapter wire mapping

The Anthropic adapter SHALL map the provider-neutral `ModelRequest` and `ModelChunk` constructs onto the Anthropic Messages API and back: `CacheBreak` blocks become ordered `cache_control` markers capped by `Caps.CacheBreakpoints`, an Anthropic `stop_reason` becomes a `FinishReason`, a `ResponseSchema` is honoured through a forced tool call only when the profile declares `Caps.Constrained`, and an opaque `Reasoning` signature round-trips to the same endpoint.

#### Scenario: cache control markers mapped in order

- WHEN a request carries three `CacheBreak` blocks and the selected profile has `CacheExplicit` with `CacheBreakpoints` 2
- THEN the adapter sends two `cache_control` markers in block order and drops the third

#### Scenario: stop reason mapped to finish reason

- WHEN the provider stream ends with `end_turn`, `tool_use` or `max_tokens`
- THEN the final chunk carries `FinishStop`, `FinishToolUse` or `FinishMaxTokens` respectively

#### Scenario: response schema honoured through forced tool

- WHEN a `ResponseSchema` reaches a profile that declares `Caps.Constrained`
- THEN the adapter sends one forced tool carrying the schema and returns the tool arguments as the response

#### Scenario: reasoning signature round-trips

- WHEN the provider returns an opaque `Reasoning` block that carries a signature
- THEN the block is retained with its signature and re-sent unchanged to the same endpoint
