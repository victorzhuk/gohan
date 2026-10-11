# Proposal: protect-tool-output-storage

## Why

Sandbox output is stored before it is sanitized. `withWorkspace` runs the tool body first and only then calls `redactSecrets` (`std/sandbox/percall.go:26-31`), while the body's `spiller` has already written the raw stream bytes to the `OutputStore` (`std/sandbox/output.go:39-47`). The stored document therefore keeps injected secret values, and the `Ref` a result carries lets `read_output` serve them back into history — the exact re-entry the capability rules forbid. `web_fetch` writes a second gap: it returns the whole response body inline and never applies `ToolSpec.MaxOutput` or an `OutputStore` (`std/tool/http/fetch.go:20-38`), so an unbounded, unspilled body reaches the model.

## What Changes

- Sanitize a stream before any byte is persisted or excerpted. `spiller.spill` MUST redact injected values first, then cap, then store the redacted bytes and build the head excerpt from them (`std/sandbox/output.go:39-70`).
- Keep the leak count route-independent: every occurrence of an injected value counts once, whether the bytes stay inline or spill, and the second safety-net pass over already-redacted blocks MUST NOT count again (`std/sandbox/secrets.go:58-93`).
- Make the no-store overflow explicit instead of returning the whole body: a stream over the cap with no `OutputStore` wired returns a bounded sanitized excerpt plus a marker naming the dropped bytes and the missing store.
- Cap and spill the `web_fetch` body through the same convention, so the response is bounded by `ToolSpec.MaxOutput` and stored when a store is wired (`std/tool/http/fetch.go:20-38`).
- Reuse one existing cap-and-store convention. The current `exec.OutputCapper` logic (`std/tool/exec/output.go`) and the sandbox `spiller` logic are the same shape; the shared step moves to the package that already owns the store read path, and both callers delegate to it.
- Move the cap default to the package that owns the shared step: `std/outputs.DefaultMaxOutput = 64 * 1024` becomes the single source, and the exported `exec.DefaultMaxOutput` and `sandbox.DefaultMaxOutput` are removed with no alias and no shim.
- **Not breaking:** `http.Fetch(policy)` keeps its current one-argument call shape through a variadic option, and the sandbox policy, port and option signatures are unchanged. The two relocated exported default constants are the deliberate exception.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `sandbox`: three new focused requirements make secret sanitization a precondition of persistence and excerpting, fix leak counting, and define overflow behaviour without a store. They do not restate the broad *Limits and outputs* requirement, which another package owns.
- `tools`: two new focused requirements make the `web_fetch` inline cap and store spill normative, and state that the HTTP status line counts against the cap, without restating the broad *Egress* requirement.

## Impact

The package touches `std/sandbox` (`output.go`, `secrets.go`, `percall.go`, `sandbox.go`), `std/tool/http/fetch.go`, the shared cap-and-store step beside `std/outputs/read_output.go`, and `std/tool/exec/output.go` as a delegating caller. The cap default relocates to `std/outputs`: `exec.DefaultMaxOutput` and `sandbox.DefaultMaxOutput` are removed, and every caller reads `outputs.DefaultMaxOutput`. That symbol removal is a breaking change for any external reference to those two constants, taken deliberately so the default has one source and no alias. No store interface, port, value type or stored shape changes; `MemoryOutputs` is process-local, so no data migration is needed. PostgreSQL store ownership, the `PerSession` lifecycle, the generic driver output path, and the graph, adk, provider and sandbox-adapter scenarios stay out of scope; the main-spec edits, the metric registry entry and any ADR land at apply time.
