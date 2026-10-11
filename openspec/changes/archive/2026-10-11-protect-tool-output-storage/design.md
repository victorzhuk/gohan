# Design

## Context

See proposal.md - Why. Two shipped paths write model-visible tool output:

- `std/sandbox`: `withWorkspace` resolves secrets, runs the body, then redacts the returned blocks (`std/sandbox/percall.go:16-31`). The body already wrote raw bytes through `spiller.spill` → `OutputStore.Put` (`std/sandbox/output.go:39-47`). The blocks `redactSecrets` rewrites are the inline ones; the stored document is never touched, and `read_output` (`std/outputs/read_output.go:29-56`) reads that document back.
- `std/tool/http`: `Fetch` returns `HTTP <status>\n<body>` in one `Text` block with no cap and no store (`std/tool/http/fetch.go:20-38`).

Two cap-and-store implementations already exist: `exec.OutputCapper` (`std/tool/exec/output.go:19-65`) and `sandbox.spiller` (`std/sandbox/output.go:16-71`). They differ in the no-store branch: `OutputCapper` drops and marks with an empty ref, the spiller returns the whole body inline. The store port is `stores.OutputStore` with content-addressed refs (`core/stores/outputs.go:24-30`). No driver-level capping exists: `defaultMaxOutput` is only the `NewTool` spec default (`core/tool.go:26-28,180`), and no core step caps a returned tool result.

## Goals / Non-Goals

**Goals:**
- Make secret sanitization a precondition of every byte that reaches the `OutputStore` or an inline excerpt, so `read_output` cannot re-enter a value.
- Make leak counting independent of the inline/spill route.
- Bound `web_fetch` by `MaxOutput` and spill its body by the same convention.
- Define overflow behaviour when no store is wired.

**Non-Goals:**
- No generic output framework: no new interface, registry, policy type or stage list.
- No change to the sandbox port, policy, options, `ToolSpec`, `OutputStore` or stored shape.
- Not the PostgreSQL store, the `PerSession` lifecycle, the driver output path, or the graph, adk, provider and sandbox-adapter scenarios.

## Decisions

**D1. Sanitize at the spill boundary, not after the body.** `spiller.spill` takes a sanitizer derived from the resolved secrets and applies it to the bytes first, then caps, then stores and excerpts from the sanitized bytes. The existing post-body `redactSecrets` pass stays as a safety net over the returned blocks but becomes idempotent and does not double-count. *Alternative:* keep post-body redaction and also rewrite the stored document after `Put` — rejected, the store is content-addressed and already holds the raw bytes; a rewrite needs a second write and a new ref, and leaves a raw ref live in the meantime.

**D2. One reusable cap-and-store step.** The step (cap check, sanitize hook, conditional `Put`, head excerpt, truncation marker, ref, `gohan.output.stored` count) moves to `std/outputs` beside `read_output.go` as a single function. `exec.OutputCapper.Cap` and `sandbox.spiller.spill` delegate to it; `http.Fetch` calls it. This is a relocation of the existing convention, not a new abstraction: no interface, no options type, no registry. *Alternative:* a third in-package copy in `std/tool/http` — rejected, three copies of the cap boundary drift. *Alternative:* cap every tool result centrally in the driver — rejected, it widens the change to every tool path and the model-request builder, and reads as the generic output framework this change excludes.

**D3. No-store overflow is bounded and explicit.** Over the cap with no store, the step returns the sanitized head excerpt plus a marker naming the dropped byte count and the missing store, and produces no ref. It never returns the whole body and never persists. The spiller's current whole-body fallback (`std/sandbox/output.go:44`) is removed. *Alternative:* fail the call — rejected, it discards output the model can still use. *Alternative:* inline the whole body — rejected, unbounded prompt and memory growth and the "silently persisting unsafe bytes" path.

**D4. `web_fetch` gains a variadic option, not a new signature.** The exported surface is declared here, not left to implementation:

- `type FetchOption func(*fetchConfig)`.
- `type fetchConfig struct { store stores.OutputStore; tel types.Telemetry }`, unexported.
- `func Fetch(policy types.EgressPolicy, opts ...FetchOption) (gohan.Tool, error)`.
- `func WithOutputStore(store stores.OutputStore, tel types.Telemetry) FetchOption` — sets both fields. `store` and `tel` may each be nil; a nil store selects the D3 no-store branch and a nil telemetry pointer skips the count.

Existing `http.Fetch(policy)` calls compile unchanged. Inline cap comes from the tool's own `ToolSpec.MaxOutput`, so no per-call cap option is added. *Alternative:* a required store parameter — rejected, it breaks the public call shape for users with no store.

The cap applies to the complete rendered text, which is the HTTP status line followed by the body. The status prefix is part of the capped bytes, so the prefix cannot push inline content past `MaxOutput`, and the stored document is that same rendered text. A response whose rendered text fits the cap stays whole inline with no ref and no `gohan.output.stored` count; only a larger response is capped, stored, or marked as dropped. *Alternative:* cap the body and append the status line afterwards — rejected, the delivered bytes would exceed `MaxOutput`.

**D4a. The shared step is one function with declared types.** The step in `std/outputs`, beside `read_output.go`, is:

```
const DefaultMaxOutput = 64 * 1024

type SanitizeFunc func(ctx context.Context, stream string, data []byte) (clean []byte, leaks int, err error)

type OutputStoreParams struct {
	Store      stores.OutputStore
	Run        types.RunInfo
	Tool       string
	Max        int
	Stream     string
	Data       []byte
	ForceStore bool
	Sanitize   SanitizeFunc
	Tel        types.Telemetry
}
```

`std/outputs` owns `DefaultMaxOutput = 64 * 1024`, the single source for the cap default. `Max` at or below zero resolves to `outputs.DefaultMaxOutput`; a positive `Max` is unchanged. The exported defaults `exec.DefaultMaxOutput` and `sandbox.DefaultMaxOutput` are removed with no alias, no shim and no second copy, and every caller imports `std/outputs` for the default. This is a breaking symbol relocation: this change does not keep every exported symbol of the touched packages unchanged. *Alternative:* keep the default in `exec` and let `std/outputs` import `std/tool/exec` — rejected, the delegation makes `exec` import `outputs`, so the reverse import closes the reported cycle. *Alternative:* copy or alias the constant into both caller packages — rejected, copies keep several sources of truth and aliases are excluded by the clean cutover.

`Data []byte` carries the stream bytes into the step. The step has no other input for the content, so without `Data` it cannot sanitize, cap or excerpt the stream.

`ForceStore bool` tells the step to store the sanitized bytes and return a ref even when they fit the cap. `sandbox.spill` sets it when the adapter truncated output or returned files, so the existing stored `File` references stay live and readable. Without `ForceStore` those references would disappear.

Order: sanitize `Data` once when `Sanitize` is non-nil, resolve the effective cap, `Put` the sanitized bytes when `ForceStore` is true or when the sanitized length exceeds the effective cap, render the sanitized excerpt and the marker, then count. `StoreOutput` returns the blocks, the ref (`""` when nothing was stored), and the error.

Leak counting is separate from leak computation. `Sanitize` computes the number of injected values found in the raw bytes, returns it once, and emits nothing. `StoreOutput` emits that returned count once through `types.TelemetryFrom(ctx)` as `gohan.sandbox.secret_leak` with no new labels. The sandbox safety-net pass owns the second, idempotent count over returned blocks only, and adds only occurrences it actually replaces. A nil `Sanitize` is valid, changes no bytes, and leaks nothing.

Errors. A `Sanitize` error returns the error with no blocks and no ref, and no `Put` occurs. `StoreOutput` returns the underlying `Put` error unchanged; each caller keeps its own existing wrapping — `exec` keeps `fmt.Errorf("exec: store %s output: %w", stream, err)` and `sandbox` keeps `fmt.Errorf("store %s: %w", stream, err)`.

Blocks. The step returns `[]types.Block`; it does not adapt them further. The sandbox adapter keeps its existing empty-stream result, keeps model origins on the stored excerpt and on `File` blocks, appends the `File` block only when the returned ref is nonempty, and keeps the post-body safety pass.

Markers and counts. The stored marker stays `\n[%s truncated at %d bytes, %d bytes dropped; full output ref: %s]` and the no-store marker stays `\n[%s truncated at %d bytes, %d bytes dropped; no output store configured]`. Dropped bytes are `max(len(clean)-effectiveMax, 0)`, so a forced store of bytes that already fit never reports a negative drop count. `gohan.output.stored` is emitted exactly once after a successful `Put`, keeps `types.String(types.KeyToolName, Tool)`, and a nil `Tel` emits no stored count.

*Alternative:* an interface with `Cap`/`Spill` methods — rejected, D2 excludes a new interface.

**D5. Deterministic marker, route-independent count.** The marker is the existing digest-derived `secretToken` (`std/sandbox/secrets.go:49-52`), so equal bytes sanitize to equal bytes and content-addressed refs stay stable. The boundary count is computed once over the raw stream before sanitization, summed across all injected values, returned by `Sanitize`, and emitted once by `StoreOutput`, regardless of how many bytes end inline or in the store. The safety-net pass counts only when it actually changes bytes, so an occurrence is never counted twice.

## Risks / Trade-offs

- Sanitized bytes change the stored document, so a ref minted before this change differs from one minted after → `MemoryOutputs` is process-local and there is no persisted store, so no migration is needed; the erasure path already drops refs by session and subject (`core/erasure.go:65`).
- The marker is longer than most values, so sanitization can grow bytes and shift the cap boundary → the cap and the excerpt are measured after sanitization (D1, `sandbox.excerpt-cap-boundary`).
- `web_fetch` now drops bytes when no store is wired where it previously returned them → the marker makes the drop explicit and the behaviour matches the sandbox rule.
- Reusing one step risks changing `exec` output behaviour → the delegated step keeps the current `OutputCapper` marker text and the `gohan.output.stored` label, and the exec tests stay the guard.
- Relocating the exported default constants breaks any external reference to `exec.DefaultMaxOutput` or `sandbox.DefaultMaxOutput` → the move is deliberate, the default keeps the same value, and no alias is kept, because a second name for one constant is a second source of truth.
- The digest marker is longer than a short injected value, so a plain substring scan can match inside a marker the boundary pass already wrote → the safety-net pass treats generated markers as opaque while scanning, and short and overlapping values are regressed without any real credential.

## Migration Plan

No stored-shape or port change, and no data migration. Apply order: the shared step, then `std/sandbox` (sanitize-before-persist and no-store branch), then `std/tool/http`. Rollback is a revert; no state is produced by the wiring. Main-spec edits to `openspec/specs/sandbox` and `openspec/specs/tools`, the `gohan.output.stored`/`gohan.sandbox.secret_leak` registry entries and any ADR land at apply time.

`docs/design/types.md` regeneration is mandatory at apply time, not optional. D4 exports `FetchOption` and `WithOutputStore` from `std/tool/http`, and D4a exports `DefaultMaxOutput`, `SanitizeFunc`, `OutputStoreParams` and `StoreOutput` from `std/outputs`. Those are six new exported symbols and two removed ones, `exec.DefaultMaxOutput` and `sandbox.DefaultMaxOutput`, so the types index changes and `task spec:types` must regenerate it in the same commit. The regeneration must drop the two obsolete default entries. A stale index fails the types gate.
