# Proposal

## Why

The audit demonstrated that the spec coverage wrapper masks intermediate leg failures. `Taskfile.yml:17-27` (`spec:coverage`) and `:33-44` (`spec:gate`) run eight `go test -json` legs inside a `{ ...; } | python3 tools/spec_coverage.py` group. Bash suppresses errexit for every command inside a pipeline's left-hand group, and the group's exit status is the LAST leg's status — so a middle leg that fails setup or fails tests neither aborts the run nor appears in the output. The coverage tool counts only `pass` events (`tools/spec_coverage.py:37-41`), so a failed leg silently becomes "missing scenarios" attributed to the registry instead of to the build.

Demonstrated during the audit: adapter/otel hit a transient setup failure (an unresolvable import in a mid-edit `export.go`), and the examples leg needs uncached third-party modules offline; the run still printed a full summary with `missing_in_scope=36`, and 10 of those 36 IDs were leg-failure artifacts, not record defects. The gate's red verdict was correct, but its evidence misattributed the cause, costing a full per-ID attribution pass to disentangle.

## What Changes

- Make each leg's failure visible: capture per-leg exit status inside the group so any failed leg aborts the pipeline with its name (e.g. `go test ... || { echo "leg failed: <name>" >&2; exit 1; }`), in both `spec:coverage` and `spec:gate`.
- Teach `tools/spec_coverage.py` to report build/setup failures from the JSON stream (`Action: "fail"` events with no `Test` field, and `Output` events carrying `[setup failed]`) as a distinct `failed_legs` section, so even a tolerated failure is named in the summary.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. Tooling only; no spec behavior changes. Archives with `--skip-specs`.

## Impact

`Taskfile.yml` and `tools/spec_coverage.py` (plus its unit tests under `tools/`). No registry, spec or scenario changes. The gate's pass/fail semantics are unchanged: a failed leg still fails the run, but now it names itself.

Dependencies: none. Land before `scenario-record-reconciliation`'s gate task so its acceptance evidence is trustworthy.