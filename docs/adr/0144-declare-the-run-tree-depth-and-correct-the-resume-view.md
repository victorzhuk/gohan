# 0144 - Declare the run tree depth and correct the resume view

Status: accepted
Date: 2026-10-04

## Context

Two specs disagreed with the floor. `subflows/spec.md` requires a run to carry
`Depth = parent + 1`, while the `RunInfo` shape in `identity/spec.md` has no `Depth` field - the
only depth in the corpus sits on `EventMeta`. And `recovery/spec.md` declares
`RunView{stores.Run; Input *suspension.ResumeInput}`, naming a `suspension` package that has
never existed: the type landed in the floor as `stores.ResumeInput`.

## Decision

`RunInfo` gains `Depth int`, in the spec and in `core/types/identity.go`, and every run that
records its root and parent records its depth with them. `RunView.Input` is
`*stores.ResumeInput`.

## Consequences

The tree can be grouped and charged without inventing a second resume type, and a spec no
longer points at a package that does not exist. Anything that later wants a `suspension`
package - the vocabulary is already in the floor - will have to justify a move rather than
assume one.
