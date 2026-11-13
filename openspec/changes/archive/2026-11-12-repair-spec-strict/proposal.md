# Proposal: Repair strict spec validation

## Why

The main specs fail strict validation. Requirement bodies lack `MUST` or `SHALL`, `tool/exec` has an empty requirement, and some requirements have no scenarios.

Current baseline from `openspec validate --specs --strict 2>&1 | tail -3`:

```text
  ✗ [ERROR] requirements[1]: Requirement "Notes, outputs and shared state" must contain SHALL or MUST
Totals: 2 passed, 34 failed (36 items)
Details: openspec validate agui --type spec
```

## What Changes

- Add `MUST` or `SHALL` to flagged requirement bodies without changing their semantics.
- Promote the existing `tool/exec` contract into its empty requirement and move the three already registered exec scenarios into it.
- Split every requirement body above 500 characters into separate requirements that each state one behavior. Existing headings survive; no scenario, ID, registry row or test changes.
- Edit the main specs directly. This change has no spec deltas and is archived with `--skip-specs`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

Main specs named by strict validation receive structural repairs only. Their contracts do not change.

## Impact

No new spec content, invented scenario semantics, code changes, test changes, or scenario-ID renames. The target is 36 of 36 main specs passing strict validation.
