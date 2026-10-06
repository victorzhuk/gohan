# Design

## Context

The extraction half of the taint contract is written but unbuilt. Rule 4 names
`std/flow.Quarantine[Out]` as `Extract` with no tools over untrusted content and
requires its output to keep the origins of the blocks it was extracted from
(`openspec/specs/taint/spec.md:50`). The registered scenario
`taint.quarantine-carries-origin` is the only public proof of that rule
(`openspec/specs/taint/spec.md:129-133`), and the M1 matrix records it as
`missing` because no `Quarantine` constructor exists
(`openspec/changes/m1/design.md:48`).

What ships today, read this session:

- `std/flow` publishes the recipes `Extract[Out]` and `Classify[L]`, plus the
  `WithRepairs` option (`std/flow/extract.go:30`); no `Quarantine` exists. Two
  single-call recipes that never offer a tool (`std/flow/extract.go:1-5`, `:45`, `:78`; the
  flow contract table lists the same two, `openspec/specs/flow/spec.md:86-87`).
- The taint matcher and its window already exist: `std/taint.New` builds the
  hook, `evaluate` walks the arguments, the window drops `OriginSystem` and
  `OriginUser` blocks and keeps the rest (`std/taint/taint.go:36`, `:57`, the drop
  rule at `:71` with `:62` as the surrounding filter), and `MinMatch` defaults to 16
  (`std/taint/taint.go:27`). `afterLastUser` (`std/taint/taint.go:113`) truncates
  the window at the last user turn.
- `Origin{Kind, Name}` and `BlockBase.Origin` already exist on every block
  (`core/types/message.go:27-46`), and `ToolResult` embeds `BlockBase`
  (`core/types/message.go:106-113`).
- Provenance on a tool result is set by the call path, never by the tool author:
  `res.Origin = types.Origin{Kind: types.OriginTool, Name: call.Name}`
  (`core/drive_turn.go:944-948`).
- `NativeSpec.TaintPolicy` carries the flow's hook and `Build` resolves it
  (`core/native_options.go:15-38`), so enforcement needs no work here.

Missing today: the `Quarantine` constructor, a value that carries the source
origins out of a recipe, and the recording rule that puts those origins on the
blocks a session later reads.

### Milestone conflict, resolved by citing the record

`taint.subflow-carries-origin` is settled as **moved to M3**
(`openspec/changes/m1/design.md:75`): the scenario names a `FlowAsTool` child,
`FlowAsTool` belongs to `subflows` (M3), and no public child-flow entry point
exists in M1. §2.3 records that `m1-extraction-provenance` keeps
`taint.quarantine-carries-origin` only and cites that section as the reason the
subflow case is absent (`openspec/changes/m1/design.md:81`). This design does
exactly that: the subflow scenario stays verbatim in the delta because it is
registry-bound (`openspec/scenarios.json:1537-1542`), and its behaviour is not
built here.

## Goals / Non-Goals

**Goals:**

- A quarantined extraction carries the origin of the content it read.
- A later tool call whose argument contains the extracted substring is tainted
  by the existing window rule, so the default policy forces `Ask` for a
  side-effecting call.
- `taint.quarantine-carries-origin` passes on a public path.

**Non-Goals:**

- No child-flow entry point. `FlowAsTool`, `MapReduce` and their origin
  propagation stay in M3 (`openspec/changes/m1/design.md:75-81`).
- No change to `std/taint`: the matcher, the window, the policy and the gate
  order are landed and are consumed unchanged (`std/taint/taint.go:36-116`).
- No new taint metric: the metric set is closed and this change reports none
  (`openspec/specs/taint/spec.md:56`).

## Decisions

**D1 — The carrier is a typed value in `core/types`.** Add
`types.Quarantined[Out]{Value Out; Origins []Origin}`. `Value` is the decoded,
validated extraction; `Origins` are the distinct origins of the input blocks the
extraction read. A wrapper, not a bare `Out`, because rule 4 says the *value*
carries the origins and Go has no other place to put them without inventing a
second channel. Alternative rejected: returning `[]types.Block` loses the typed
value the recipe contract promises; alternative rejected: a `Stack` method with
type parameters is illegal in Go.

**D2 — `std/flow.Quarantine[Out]` is `Extract` plus provenance.** Signature
`func Quarantine[Out any](s *gohan.Stack, profile string, opts ...Option) gohan.Flow[[]types.Block, types.Quarantined[Out]]`,
implemented over the existing decode and validation path of `Extract`
(`std/flow/extract.go:38-70`): one governed model call, strict derived schema,
no tool offered, the same bounded repair turns. It additionally computes
`Origins` from its input blocks: the distinct `BlockBase.Origin` values of the
blocks whose text supplied the extraction. Extraction is one call over all
input blocks, so the recipe reports the origin of every untrusted input block it
read; it never consults the model for an origin and never drops one.

**D3 — The attach site is the recording path, not the recipe.** A recipe returns
a Go value; a session window holds blocks. The change adds the recording entry
point `gohan.RecordQuarantined[Out](ctx context.Context, s *Stack, sessionID string, q types.Quarantined[Out]) error`
in `core`, which appends the result as one `ToolResult` block per distinct
origin: `BlockBase.Origin` is that origin, `Content` is one `Text` block of the
canonical JSON of `q.Value`, and `Outcome` is `Succeeded`. This follows the
existing rule that the call path sets provenance and a tool author does not
(`core/drive_turn.go:944-948`). Alternative rejected: stamping all origins onto
one block, because `BlockBase.Origin` is a single `Origin`
(`core/types/message.go:49-52`) and a second origin would be silently lost.

**D4 — The readback is rule 1, unchanged.** The recorded blocks carry an origin
whose kind is neither `OriginSystem` nor `OriginUser`, so the window keeps them (the drop rule at `std/taint/taint.go:71`, with `:62`
as the surrounding filter), and a later call whose argument contains the
extracted substring is tainted with that origin (`openspec/specs/taint/spec.md:47`).
No new matching rule, no new gate step, no change to `std/taint`.

**D5 — Public path for the scenario.** The proof is Go-level, matching how a
recipe is used today: read the PDF blocks through a tool whose result blocks
carry `OriginTool{Name: "read_pdf"}`, run
`Quarantine[Invoice]` over them, record the result with
`RecordQuarantined`, then run the agent flow whose model calls `pay_supplier`
and observe the suspension with `ArgOrigins["iban"].Name == "read_pdf"`. The
M3 `FlowAsTool` path will reuse D1 and D3 rather than re-invent a carrier.

## Risks / Trade-offs

- [The origin a quarantine reports is the input block's origin, so a document
  block that itself arrived trusted yields a trusted result] → Correct by
  construction: a `OriginUser` input produces a result excluded from the window,
  which is the intended trust boundary, and the scenario uses untrusted input.
- [Two recipes now share the decode and repair path] → `Quarantine` is built on
  the existing `Extract` internals (`std/flow/extract.go:38-70`) rather than a
  copy, so repair behaviour stays single-sourced.
- [A Go-level proof does not exercise a model-invoked child flow] →
  Acknowledged and deliberate: the child-flow case is M3
  (`openspec/changes/m1/design.md:75`), and this design records the carrier it
  will reuse.

## Migration Plan

Additive. Nothing calls the new constructor yet, no existing signature changes,
and no stored shape changes, so there is no rollback beyond reverting the
commit. The two delta spec requirements are applied to
`openspec/specs/taint/spec.md` and `openspec/specs/flow/spec.md` when the change
is applied, per `openspec/project.md`, *Change workflow* step 5.

## Open Questions

None.
