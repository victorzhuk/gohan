# Embedded languages and definitions

Capability: `languages` · Spec v1.0 baseline (restructured from gohan-spec v0.13; ADR-0085 owns the Lang port and capability set) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `languages` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 7.9 Embedded languages: definitions, expressions, scripts

Three tiers, one contract each:

| Tier | What it is | v1 | Requirement on the language |
|---|---|---|---|
| Definitions | `core/flowdef` data model, compiled by `std/flow.Compile` into recipes | yes | a parser producing `flowdef.Definition` |
| Expressions | routing and transforms over run `Data`, evaluated by an `ExprLang` | yes | `Deterministic + StepLimit` (+ `TypeCheck` preferred) |
| Scripts | Turing-complete programs calling governed components via injected host functions, resumable across suspension | later | `Hermetic` + (`Deterministic` for replay over journaled host calls, or `Snapshot` / `Continuations` for native suspension) |

```go
type Capability uint

const (
	Deterministic Capability = 1 << iota
	StepLimit
	MemoryAccounting
	TypeCheck
	Hermetic
	Snapshot
	Continuations
)

type Lang interface {
	Name() string
	Capabilities() Capability
}

type ExprLang interface {
	Lang
	Compile(src string, env ExprEnv) (Program, error)
}

type Data map[string]any
type Value any

type ExprEnv struct {
	Vars  map[string]string
	Funcs []string
}

type CostEstimate struct {
	Steps int
	Bytes int
}

// (illustrative) the flowdef data model; step vocabulary is prose below
type Definition struct {
	Name    string
	Version string
	Steps   []map[string]any
}

type ExprBudget struct {
	Steps    int
	Bytes    int
	Duration time.Duration
}

type Program interface {
	Eval(ctx context.Context, data Data, budget ExprBudget) (Value, error)
	Cost() CostEstimate
}

type DefinitionParser interface {
	Lang
	Parse(src []byte) (flowdef.Definition, error)
}
```

`flowdef.Definition` (borrowed from the Serverless Workflow vocabulary, trimmed to gohan's seams): `call` (flow | tool | model by registered name, typed `In`/`Out`, optional `approval: required` → forces `Ask`), `do`, `for` (with a mandatory `max`), `fork` (with a mandatory `parallelism` and an `onError: failFast | collect`, default `failFast`), `switch` (expression or `Classify`), `try` (step-level catch → fallback step; model/tool retries stay in the chains), `wait` (→ `Scheduled`), `listen` (→ `AwaitingExternal` with a correlation key), `set` (expression), `raise`. Each step has `input`/`output` expressions over a run-scoped `Data` object; a step may not select a tool, flow or model by a value that is tainted (`taint`). Steps map onto `std/flow` recipes (`Pipeline`, `MapReduce`, `Route`, …) and the agent flow; nothing in a definition or expression can call a tool, a model or `suspend` directly.

`Build`: parse with the declared front-end → validate every `call` against the registry (`stack.RegisterFlow(name, flow)`, tools by name) including `In`/`Out` schemas → compile expressions with the configured `ExprLang` and reject any referencing host calls → hash the canonical JSON form into the manifest (ADR-0081) → `Explain` prints the compiled definition in canonical form. `Build` refuses an `ExprLang` lacking `Deterministic + StepLimit`, and a `ScriptLang` role entirely in v1.

Adapters: `adapter/cel` (default `ExprLang`; non-Turing-complete, linear evaluation, cost estimate and runtime cost limit, type-checked against the `Data` schema); `adapter/lispico` (definition front-end; `ExprLang` if go-lispico declares `Deterministic + StepLimit` for a restricted evaluation mode; the `Continuations` route for scripts is the natural Lisp option and is tracked in Q21); `std` ships the JSON/YAML front-end with no dependencies. `adapter/starlark`, `adapter/goja`, `adapter/wasm` are possible later under the same matrix and none is required.


## Requirements

### Requirement: Definitions and languages

#### Scenario: same definition, two front-ends
ID: `languages.same-definition-two-front-ends`
- WHEN the same flow is written as YAML and as Lisp
- THEN both parse to an identical canonical `flowdef.Definition` and the manifest hash is the same

#### Scenario: unknown call rejected
ID: `languages.unknown-call-rejected`
- WHEN a definition calls flow `pricing.v2` that is not registered
- THEN `Build` fails naming the step and the missing name

#### Scenario: schema mismatch rejected
ID: `languages.schema-mismatch-rejected`
- WHEN a `call` output does not match the next step's declared input schema
- THEN `Build` fails naming both steps

#### Scenario: expression cannot escape
ID: `languages.expression-cannot-escape`
- WHEN an expression references a function not in the `ExprEnv`
- THEN compilation fails at `Build`

#### Scenario: expression budget
ID: `languages.expression-budget`
- WHEN an expression's `Cost()` exceeds the flow's `ExprBudget`
- THEN `Build` rejects it; WHEN runtime evaluation exceeds the budget THEN the step fails `Permanent`

#### Scenario: language admission
ID: `languages.language-admission`
- WHEN a `Lang` without `StepLimit` is configured as `ExprLang`
- THEN `Build` fails with `ErrLangNotAdmitted`

#### Scenario: unbounded loop rejected
ID: `languages.unbounded-loop-rejected`
- WHEN a `for` step has no `max`
- THEN `Build` rejects the definition

#### Scenario: approval on a definition step
ID: `languages.approval-on-a-definition-step`
- WHEN a `call` step declares `approval: required`
- THEN the gate asks regardless of the tool's `Effect`, and the `ApprovalRequest` names the definition and step
