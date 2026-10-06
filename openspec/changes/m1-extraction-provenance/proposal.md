# Proposal

## Why

The `taint` contract says an extracted value keeps the provenance of the content
it was extracted from (`openspec/specs/taint/spec.md:50`, rule 4), and
`taint.quarantine-carries-origin` is the registered scenario that binds it
(`openspec/specs/taint/spec.md:129-133`). No carrier ships: `std/flow` publishes the recipes `Extract[Out]` and
`Classify[L]`, plus the `WithRepairs` option (`std/flow/extract.go:30`); no
`Quarantine` exists (`std/flow/extract.go:1-5`, `:45`, `:78`) and the
M1 matrix records the scenario as `missing` because no `std/flow.Quarantine`
exists (`openspec/changes/m1/design.md:48`). The enforcement that the extracted
value must survive already landed (`std/taint` matcher and window,
`std/taint/taint.go:36-116`), so the only gap is the carrier.

## What Changes

- Add `std/flow.Quarantine[Out]`: the `Extract` recipe restricted to untrusted
  content, offering no tool, returning the decoded value with the origins of the
  blocks it read (`openspec/specs/taint/spec.md:50`).
- Add the carrier `types.Quarantined[Out]{Value, Origins}` and the recording
  entry point that attaches those origins to the recorded result blocks, so the
  taint window reads them back by rule 1 (`openspec/specs/taint/spec.md:47`).
- Register the recipe in the `flow` contract beside `Extract` and `Classify`
  (`openspec/specs/flow/spec.md:86-87`).
- Resolve the milestone conflict: this change keeps
  `taint.quarantine-carries-origin` only. `taint.subflow-carries-origin` stays
  with M3 (`openspec/changes/m1/design.md:75`, `:81`).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `taint`: the *Taint survives extraction* requirement gains its normative text
  and keeps both of its scenarios (`openspec/specs/taint/spec.md:127-133`).
- `flow`: the *Flow contract* requirement names the recipe surface, including
  `Quarantine` and its typed carrier (`openspec/specs/flow/spec.md:117`).

## Impact

`std/flow` gains one recipe and `core/types` gains one carrier type; the session
recording path gains the attach rule that stamps the source origins onto the
recorded result blocks, following the existing rule that the call path, not the
tool author, sets provenance (`core/drive_turn.go:944-948`). `std/taint` is
unchanged: the matcher already honours a block whose `Origin.Kind` is neither
`OriginSystem` nor `OriginUser` (the drop rule at `std/taint/taint.go:71`, with
`:62` as the surrounding filter; `afterLastUser` at `:113` truncates the window
at the last user turn). No release
is cut and no capability spec outside these two is touched.
