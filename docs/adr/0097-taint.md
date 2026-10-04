# ADR-0097: Information-flow control at the tool boundary

Status: accepted · Origin: grill round 23 (2026-09-29)

## Decision

`ToolSpec.Capabilities{Exfil, PrivateRead}` classify tools; `Build` detects the lethal trifecta per flow and fails unless a `TaintPolicy` is declared. Taint is computed deterministically before the permission gate: an argument is tainted when its value or a substring ≥ 16 bytes occurs verbatim in a non-system, non-user block since the last user turn. The gate applies `TaintPolicy{ToExfil: Deny, ToSideEffect: Ask, ToReadOnly: Allow}` after hard blocks and before grants; `Deny` fails the call, `Ask` forces approval with tainted arguments highlighted. `std/flow.Quarantine` (extract with no tools) and sub-flow outputs keep source origins, so a value never becomes trusted by passing through a model. `flowdef` steps cannot select tools by tainted values. Guards remain the detection layer.

## Context and evidence

Provenance already existed on every block and on approvals, but nothing enforced it. The field's conclusion is that filtering cannot be the boundary and that removing one leg of the trifecta, or blocking tainted values at the tool boundary, gives a property rather than a rate. A full CaMeL interpreter is deferred to the script tier (Q26).

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

New capability `taint`; `tools.ToolSpec` gains `Capabilities`; `permission` gate order gains the taint step; `languages` gains the tainted-selection rule; `guards` cross-references; `telemetry` gains `gohan.taint.*` and `gohan.build.trifecta`. Extends ADR-0040.
