# 0164. Tool output is sanitized before the shared cap and store step

Status: accepted

## Context

Three tool paths capped and stored output on their own: `std/tool/exec` through
`OutputCapper.Cap`, `std/sandbox` through `spiller.spill`, and `std/tool/http`,
which returned the rendered response whole. Each kept its own copy of the
boundary, so the copies drifted: the sandbox sanitized after the body had
already been capped and spilled, the sandbox spiller fell back to the whole
body when no store was wired, and `web_fetch` had no store path at all. The
consequence was that an injected secret could reach the `OutputStore` and a
`Ref` served by `read_output` carried it back out.

## Decision

The cap-and-store step is one function, `outputs.StoreOutput`, in `std/outputs`
beside `read_output.go`. Sanitization is not a stage of it and not a follow-up
pass over stored bytes: `Data` is sanitized first, the effective cap is resolved
next, and the cap, the excerpt and the `Put` all read the sanitized bytes. The
existing post-body `redactSecrets` pass stays as a safety net over returned
blocks, is idempotent, and counts only occurrences it actually replaces.

`std/outputs` owns `DefaultMaxOutput = 64 * 1024` as the single source for the
cap default. A `Max` at or below zero resolves to it. `exec.OutputCapper.Cap`,
`sandbox.spiller.spill` and `http.Fetch` all delegate to the shared step, and the
exported `exec.DefaultMaxOutput` and `sandbox.DefaultMaxOutput` are removed with
no alias, no shim and no second copy. This is a deliberate breaking relocation:
one constant, one name, one owner. A second name for one constant is a second
source of truth.

Overflow with no store is bounded and explicit. Over the cap with no store the
step returns the sanitized head excerpt plus a marker naming the dropped byte
count and the missing store, produces no `Ref`, and persists nothing. It never
returns the whole body inline. The sandbox spiller's whole-body fallback is
removed.

`ForceStore` makes the step store sanitized bytes and return a `Ref` even when
they fit the cap. `sandbox.spill` sets it when the adapter truncated output or
returned files, so the existing stored `File` references stay live; without it
those references would disappear.

`web_fetch` gains options rather than a new signature:
`type FetchOption func(*fetchConfig)` with an unexported
`fetchConfig{store, tel}`, `func Fetch(policy types.EgressPolicy, opts ...FetchOption) (gohan.Tool, error)`
and `func WithOutputStore(store stores.OutputStore, tel types.Telemetry) FetchOption`
setting both. `store` and `tel` may each be nil; a nil store selects the
no-store branch and a nil telemetry pointer skips the count. Existing
`http.Fetch(policy)` calls compile unchanged. The inline cap comes from the
tool's own `ToolSpec.MaxOutput`, so there is no per-call cap option.

The cap applies to the complete rendered text, which is the HTTP status line
followed by the body. The status prefix counts against `MaxOutput` and is part
of the stored document, so a prefix can never push inline content past the cap.

Leak counting is separate from leak computation. `SanitizeFunc` returns the
number of injected values found in the raw bytes, emits nothing, and
`StoreOutput` emits that count once through `types.TelemetryFrom(ctx)` as
`gohan.sandbox.secret_leak` with no new labels. `gohan.output.stored` is emitted
exactly once after a successful `Put` and keeps `types.String(types.KeyToolName, Tool)`.

Six exported symbols move under one owner: `FetchOption` and `WithOutputStore`
from `std/tool/http`, and `DefaultMaxOutput`, `SanitizeFunc`,
`OutputStoreParams` and `StoreOutput` from `std/outputs`. They belong to the
`tools` Contract and `std` owns their implementation, so the type index is
regenerated from the `tools` spec in the same commit that publishes them.