# Tasks

Risk depth: existing-service-strict for the gate tooling (the acceptance mechanism itself). A task is done only when its acceptance holds and `task tools:test` plus `task spec:types` are green.

## 1. Implementation

- [x] 1.1 Add per-leg failure capture to the `spec:coverage` and `spec:gate` groups in `Taskfile.yml`: each `go test` leg exits the group with its leg name on failure, preserving the existing `-timeout` values and the postgres `-tags full` gate leg.
  Acceptance: A deliberately broken middle leg (e.g. an unresolvable import in a scratch copy) aborts the pipeline and prints the leg name; the healthy path produces the same summary as before.
- [x] 1.2 Extend `tools/spec_coverage.py` to detect build/setup failures in the JSON stream (fail events without a Test field, `[setup failed]` output) and print a `failed_legs` section with the affected package names; add unit tests for the new parser path using synthetic event streams.
  Acceptance: `task tools:test` passes including the new cases; a stream containing a setup failure reports the package in `failed_legs` and exits non-zero even when no scenario is in scope.

## 2. Gate

- [x] 2.1 Run `task spec:coverage` on the current tree and confirm the output names every failed leg, then run `task tools:test` and `task spec:types`.
  Acceptance: The summary's missing counts are unchanged for healthy legs; failed legs are named; the tool tests and types index pass.