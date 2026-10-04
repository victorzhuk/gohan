# Release safety: shadow runs, release identity, evals

Capability: `release` · Spec v1.3 (ADR-0127) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

Makes a change to a prompt, model, tool, skill or chain observable and reversible: every run knows which release and variant produced it, a challenger can run against live traffic without side effects, and the `evals` module gates merges and samples production. Rollback itself stays with the flags provider. Out of scope: traffic splitting and automatic promotion.


## Contract

### Core: run mode

```go
type RunMode int

const (
	Primary RunMode = iota
	Shadow
)
```

var ShadowSuppressed = &ToolError{Kind: Permanent, Message: "shadow run: call suppressed"}

`AgentRun.Mode` (default `Primary`). In `Shadow`:

1. `ReadOnly` tools execute normally.
2. `Idempotent` and `SideEffect` tools are answered from the primary run's `Journal` when an entry with the same `CallKey` and matching fingerprint exists (`Replayed: true`); otherwise the call returns `ToolResult{Outcome: Failed, Error: ShadowSuppressed}` and the model continues.
3. The shadow run reads the primary session's history but appends to a shadow session `<session>/shadow/<runID>`; it never writes the primary `SessionLog`, `Journal` or `AuditLog`. Its `Runs` record carries `Mode: Shadow`; it never takes the primary lease.
4. Every shadow event and span carries `mode=shadow`; shadow cost is charged to `CostTags.Feature + "/shadow"` and counted against its own `RunLimits`, never the primary tree budget.
5. Suspensions in shadow are terminal: a shadow run that would suspend finishes with `Done{Reason: ShadowSuspended}`.

### Core: release identity

```go
type ReleaseManifest struct {
	Models      map[string]string
	Prompts     map[string]string
	Tools       map[string]string
	Skills      map[string]string
	Chains      map[string]string
	Definitions map[string]string
}

func (m ReleaseManifest) ID() string
```

`Build` computes the manifest (profile `Version`s, `PromptSet` and resolved `PromptRef` hashes, tool and skill hashes from the pinned manifest, chain preset hashes, `flowdef` definition hashes) and its `ID` (hash of canonical JSON). `RunInfo` gains `ReleaseID` and `Variant`; `Variant` is the value of the frozen rollout flag named by `agent.VariantFlag(key)` (empty when none). Both appear on every span, audit record, `Done` metadata, `Explain`, and as attributes on every `gohan.*` metric. `Stack.Manifest()` (Q15) returns the `ReleaseManifest`.

### `evals` module

- **Datasets**: cases are `{Input, Expected?, Tags}` in files with a SHA-256 recorded in the suite; a suite fails if a dataset hash changes without the recorded hash being updated. Recommended composition is documentation, not enforced.
- **Delta gating**: `evals.Gate{Baseline, HardBlock: -0.05, Review: -0.02}`; a candidate score below `Baseline+HardBlock` fails, between `HardBlock` and `Review` returns `NeedsReview`, above passes. Absolute thresholds remain available (`MinScore`).
- **Judges**: `Decider`s at temperature 0 with pinned `Version`; `evals.Judge{Repeat: 3}` takes the median and flags cases whose spread exceeds `0.10` as `Unstable`; judge fingerprint (profile version + instruction hash) is recorded in the report.
- **Shadow runner**: `evals.Shadow(champion, challenger Flow, inputs)` runs both — from recorded inputs (cassettes) or live, with the challenger in `Shadow` mode — and reports pairwise per input (score delta, tool-sequence diff, cost, latency), never aggregate-only.
- **Import from feedback**: `evals.Import(ctx, sessionID, evals.FromFeedback(name))` builds cases from the targets carrying that score; a redacted `Correction` becomes the expected output and the score is recorded as the case's provenance. `evals.Online` may weight sampling by feedback (`Rate` per score value); that selection bias is declared in the report.
- **Import**: `evals.Import(ctx, sessionID) (Case, error)` turns a session into a case through the `Redactor` (`EraseSubject` policy), preserving the tool sequence as a trajectory assertion; intended for the one-case-per-incident rule.
- **Online**: `evals.Online{Rate, Window, Judges}` is a `Done` hook that samples finished primary runs, runs judges asynchronously outside the request path, and emits `gohan.eval.score{flow, variant, judge}` and `gohan.eval.unstable`.
- Reports carry `ReleaseID`, `Variant`, dataset hash and judge fingerprint.

Metrics: `gohan.eval.score`, `gohan.eval.unstable`, `gohan.shadow.runs`, `gohan.shadow.suppressed{tool}`; all `gohan.*` metrics gain `release` and `variant` attributes.


## Requirements

### Requirement: Shadow mode

#### Scenario: side effect suppressed
ID: `release.shadow-suppressed`
- WHEN a shadow run calls `create_booking` and the primary journal has no entry for that `CallKey`
- THEN no tool code executes, the model receives `ShadowSuppressed`, and `gohan.shadow.suppressed{tool="create_booking"}` increments

#### Scenario: side effect replayed
ID: `release.shadow-replayed`
- WHEN the primary journal holds a completed entry for the same `CallKey` and fingerprint
- THEN the shadow run receives that result with `Replayed: true` and no tool code executes

#### Scenario: primary stores untouched
ID: `release.shadow-isolated`
- WHEN a shadow run completes
- THEN the primary `SessionLog` version, `Journal` and `AuditLog` are unchanged and the shadow session holds the run

#### Scenario: shadow budget separate
ID: `release.shadow-budget`
- WHEN a shadow run spends cost
- THEN the primary tree's `MaxCost` accounting is unchanged and the cost carries `Feature: "<feature>/shadow"`

#### Scenario: shadow suspension terminal
ID: `release.shadow-suspension`
- WHEN a shadow run would suspend for `HumanApproval`
- THEN it finishes with `Done{Reason: ShadowSuspended}` and no checkpoint is written

### Requirement: Release identity

#### Scenario: release id deterministic
ID: `release.id-deterministic`
- WHEN `Build` runs twice with identical inputs
- THEN `ReleaseManifest.ID()` is identical, and changing one prompt string changes it

#### Scenario: run carries release and variant
ID: `release.run-attributes`
- WHEN a run executes under rollout flag `assist_v2 = "b"`
- THEN spans, audit records, `Done` metadata and metrics carry `release=<id>` and `variant="b"`

### Requirement: Evals

#### Scenario: dataset pinned
ID: `release.dataset-pinned`
- WHEN a dataset file changes but its recorded hash does not
- THEN the suite fails before any case runs

#### Scenario: delta gate bands
ID: `release.delta-gate`
- WHEN baseline is 0.90 and candidates score 0.84, 0.87 and 0.89
- THEN the results are Fail, NeedsReview and Pass respectively

#### Scenario: judge median and spread
ID: `release.judge-median`
- WHEN a judge returns 0.6, 0.9 and 0.85 for one case
- THEN the case score is 0.85 and it is flagged `Unstable`

#### Scenario: pairwise shadow report
ID: `release.pairwise-report`
- WHEN `evals.Shadow` runs 50 recorded inputs
- THEN the report has 50 rows each with score delta, tool-sequence diff, cost and latency for both flows

#### Scenario: import skips operator turns
ID: `release.import-skips-operator-turns`
- WHEN a session with operator turns is imported without `IncludeOperator()`
- THEN no operator turn becomes an expected output and the trajectory assertion ends at the last model turn before the handoff

#### Scenario: import redacts
ID: `release.import-redacted`
- WHEN a production session containing a phone number is imported
- THEN the case holds the pseudonymised value and the original tool sequence

#### Scenario: import from feedback
ID: `release.import-from-feedback`
- WHEN a session has `thumbs: false` on `m3` with a correction
- THEN `Import(FromFeedback("thumbs"))` yields one case whose expected output is the redacted correction and whose provenance names the score

#### Scenario: online sampling off the request path
ID: `release.online-sampling`
- WHEN `evals.Online{Rate: 0.1}` is installed and 1 000 runs finish
- THEN about 100 are judged, no `Done` is delayed by a judge call, and `gohan.eval.score` carries `variant`
