# Decider

Capability: `decider` · Spec v1.0 baseline (restructured from gohan-spec v0.13; later decisions live in `docs/adr/`) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `decider` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.7 Decider

```go
type Decision[D any] struct {
	Value      D
	Confidence float64
}

type Decider[S, D any] interface {
	Decide(ctx context.Context, state S) (Decision[D], error)
}
```

Used by: permission gate, guards, router, eval scorers. Implementations: rules (confidence 1), LLM + schema (`adapter/openai`, `adapter/anthropic`), Jev.


## Requirements

### Requirement: Decider contract

#### Scenario: rules are certain
ID: `decider.rules-confidence-one`
- WHEN a rules-based decider decides
- THEN `Confidence == 1` and no model call occurs

#### Scenario: schema-validated decision
ID: `decider.schema-validated`
- WHEN an LLM decider returns output that does not match the decision schema
- THEN `Decide` returns an error, never a zero-value `Decision`

#### Scenario: decider failure defaults closed
ID: `decider.failure-defaults-closed`
- WHEN a decider used by the permission gate returns an error
- THEN the gate treats the call as `Ask`; a guard decider error is `Block`; a router decider error falls back to `Static`

#### Scenario: decider is observable
ID: `decider.observable`
- WHEN a decider decides inside a chain step
- THEN a `gohan.decide` span records decider name, confidence and the step that consulted it
