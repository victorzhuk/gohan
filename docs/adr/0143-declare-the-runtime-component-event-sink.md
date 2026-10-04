# 0143 - Declare the runtime component-event sink

Status: accepted
Date: 2026-10-04

## Context

`openspec/specs/runtime/spec.md` requires that component-level events (`TextDelta`,
`ToolStarted`, `ToolFinished`) and the turn counter reach the stream through a run-scoped sink
in `ctx`, and that the runtime never emits them itself. The requirement was normative while no
document declared the sink's shape, and the runtime is a leaf package that may not import the
driver, so the seam cannot live beside the notifier the driver owns.

## Decision

The sink is declared in the floor, `core/types`, beside the event payloads it carries: a
`Sink` interface with `Emit(ctx, Event)`, a `WithSink` constructor and a `SinkFrom` accessor.
`Drive` installs it in the ctx it propagates into component calls. A component that finds no
sink drops the event and carries on. The shape is now normative in the runtime spec.

## Consequences

The runtime depends on the floor alone, and any backend that adopts the runtime contract
receives component events the same way. A runtime that emits component events itself is a
contract violation, and a test that observes `TextDelta` on the sink is observing the
decorator, not the loop.
