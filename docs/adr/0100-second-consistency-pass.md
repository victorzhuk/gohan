# ADR-0100: Second consistency pass — used-but-undefined identifiers, scenario coverage, ADR lineage

Status: accepted · Origin: review round 26 (2026-09-29)

## Decision

Every identifier used inside a normative code block must be defined in a spec; `tools/gen_types_index.py` now fails on undefined identifiers as well as duplicates. Defined in their owning specs: `StopReason`, `FinishReason`, `ToolChoice`, `AffinityKeyStrategy`, `ExpiryAction`, `ApprovalVerdict`, `StepGranularity`, `EntityKind`, `Entity`, `Detector`, `Span`, `EraseReport`, `AssembleInput`, `Slot`, `ContextProvider`, `GuardBlockedError`, `Explanation`, `StepInfo`, `NotesStore`, `Data`, `Value`, `ExprEnv`, `CostEstimate`, `ContentMapping`; `(illustrative)` shapes for `Stack`, `Option`, `ToolOption`, `ToolInvocation`, `Definition`. Every capability has scenarios: `decider` (4), `flags` (5), `working-state` (5) added. Taint is computed over the full `SessionLog` window, never the projection. ADRs 0015, 0026, 0031, 0032, 0040, 0074 carry `Superseded by`/`Amended by` pointers. The example catalog names the capabilities each example exercises; every capability has at least one end-to-end home. The M0 change proposal scopes the 21 M0 capabilities and lists the rest by milestone.

## Context and evidence

The first index caught definitions only; a use-site index found 25 more identifiers an implementer would have had to invent, three capabilities with no scenarios, and six ADRs whose text contradicts later decisions without saying so.

## Consequences

`docs/design/types.md` gains an "Undefined identifiers" section that must read `none`; `task spec:types` fails otherwise. 294 scenarios.
