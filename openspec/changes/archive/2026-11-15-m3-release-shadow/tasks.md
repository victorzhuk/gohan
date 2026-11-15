# Tasks

Risk depth: existing-service-strict. Scenario ids are proposed here and registered by task 1.1 at implementation admission.

## 1. Contracts

- [x] 1.1 Register the proposed scenario ids (`release.shadow-traffic-replay`, `release.shadow-scripted-suite`, `release.shadow-no-caller-serving`, `release.check-manifest-driven`, `release.check-dependency-evidence`, `release.check-compat-evidence`, `release.check-fail-closed`, `release.decision-durable`) and regenerate docs/design/types.md. Acceptance: `task spec:types` clean; ids registered as proposed until admission.
- [x] 1.2 Finalize additive Go declarations for the orchestrator and the check suite from design.md, reusing the frozen `evals.Shadow`, `RunMode` and `Stack.Manifest()` contracts. Acceptance: declarations review against the frozen release and build contracts with no frozen method change.

## 2. Behavior

- [x] 2.1 Implement cassette replay orchestration and prove `release.shadow-traffic-replay` with an exact-ID public acceptance subtest and every delta assertion. Acceptance: challenger runs in `Shadow` mode; pairwise rows per input; no caller transport.
- [x] 2.2 Implement scripted-suite input and prove `release.shadow-scripted-suite`. Acceptance: each scripted case yields a pairwise row with score delta, tool-sequence diff, cost and latency.
- [x] 2.3 Implement the caller-transport refusal and prove `release.shadow-no-caller-serving`. Acceptance: attachment request fails before any run starts.
- [x] 2.4 Implement manifest-driven check enumeration and prove `release.check-manifest-driven`. Acceptance: every check names a `ReleaseManifest` entry; a check exists per declared surface.
- [x] 2.5 Implement dependency and compatibility evidence checks and prove `release.check-dependency-evidence` and `release.check-compat-evidence`. Acceptance: missing evidence fails the check naming the dependency or surface.
- [x] 2.6 Implement fail-closed gating and prove `release.check-fail-closed`. Acceptance: any failing, unscored or unrun check denies the release and is named.
- [x] 2.7 Implement the durable decision record and prove `release.decision-durable`. Acceptance: re-emission preserves identity, check results, evidence references and release attributes.

## 3. Integration

- [x] 3.1 Run assigned scenarios with process deadlines and worker bounds; unit tests only for named residual risks beyond acceptance coverage. Acceptance: all eight scenario ids pass with retained assertion-level evidence.
- [x] 3.2 Verify `task lint`, `task spec`, `task test` and affected adapter checks after inspecting wrappers; update the `[Unreleased]` changelog entry. Acceptance: gates green; changelog records shipped behavior.
