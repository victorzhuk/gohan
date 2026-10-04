# ADR-0122: Tool names — one grammar, collisions fail Build, deterministic namespaces, name is identity

Status: accepted · Origin: grill round 48 (2026-09-30)

## Decision

Tool names match `^[a-z][a-z0-9_]{0,63}$` and are never rewritten by adapters. Duplicate names in a flow, including reserved harness names, fail `Build` with `ErrToolCollision`. Imported sets are namespaced at wiring as `<ns>__<tool>`; the namespace is mandatory with more than one imported set and is part of the pinned manifest. `ToolSpec.Name` is the identity for manifest, grants, `CallKey`, taint, audit and approval scopes, so a rename is a new tool. A `Tool` value is built once with its dependencies as constructor arguments, selects tenant resources from the principal in `ctx`, and must be safe for concurrent calls; the undefined `Deps` reference is removed.

## Context and evidence

Every tool-calling API enforces `^[a-zA-Z0-9_-]{1,64}$`; namespaced names that clients truncated to the last segment produced silent collisions where the wrong capability ran, and the accepted fix is a fixed derivation rule with no truncation. Editor agents prefix MCP tools with a sanitized server id for the same reason. gohan keyed manifest, grants and audit on names with no grammar, collision or namespace rule.

## Consequences

`tools` v1.3 (`ErrToolName`, `ErrToolCollision`, *Names and identity* requirement, five scenarios), `interop` v1.2 (namespace rule, two scenarios), `identity` rule 8 wording; task 6; interop scenarios M3.
