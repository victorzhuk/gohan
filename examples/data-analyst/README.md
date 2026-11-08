# data-analyst

This offline example executes a read-only warehouse query in a governed sandbox with deny-all egress. The credential enters only through the workspace environment. Personally identifying rows are masked before results are stored or paged. One turn suspends, a second turn resumes from the durable workspace ref, and a stored output block is read in pages.

## Run it

From the repository root:

```sh
go -C examples run ./data-analyst
```

## Test it

```sh
go -C examples test ./data-analyst/...
```

Neither command reaches a network, a container runtime or an API key.

## Requirements and scenarios

| Scenario | What it proves |
|---|---|
| `sandbox.egress-denied` | Empty egress policy and denial handling are reachable; network enforcement is not proven. |
| `sandbox.secret-injected` | The credential enters through the workspace environment and stays out of history, journal and spans. |
| `sandbox.secret-leak-redacted` | A credential in output is redacted and the leak is counted. |
| `sandbox.per-session-persists` | The second turn reads the first turn's extract without another write. |
| `sandbox.snapshot-on-suspend` | The suspension checkpoint stores the snapshot ref and generation. |
| `sandbox.resume-restores` | A second stack restores the workspace from the stored ref. |
| `sandbox.replay-no-reexec` | Replay does not execute the journaled step again. |
| `sandbox.output-to-store` | Oversized output produces an excerpt and a ref to the stored body. |
| `sandbox.output-stored-redacted` | The stored body and excerpt contain no unmasked identifier or injected secret. |
| `working-state.output-paging` | Paging returns the requested slice and rejects an out-of-range window. |
| `sandbox.data-analyst-resume-and-page` | The resumed turn reads the extract without rewriting or re-executing, and pages contain no personally identifying value. |

## Offline-vs-real boundary

The sandbox adapter is a deterministic in-process fake. It records calls and returns scripted results. It does not isolate a process or enforce network behavior. The example proves composition ordering and reachability, not the runtime promises below.

| Promise | Why the fixture cannot prove it | Owner / scope |
|---|---|---|
| Deny-all egress enforcement (no TCP connect, no DNS resolution, `gohan.sandbox.egress_denied`) | Enforcement is at the adapter's network layer; a map-returning fake has no network layer | `m2-sandbox-adapter-conformance` (`sandboxtest.Sandbox` with a real `Factory`); the adapter leg is deferred past M2 with `m2-e2b-sandbox` |
| Real pandas / DataFrame execution, `MaxExecTime` process-group kill, `ExecResult.Seconds` accounting | A scripted `ExecResult` is a recording, not a process | `m2-sandbox-adapter-conformance` against `adapter/docker`; `sandbox.exec-timeout` |
| Router `Cascade` ordering and provider failover | Requires two live model adapters | Catalog scope: excluded, `build` capability owns `Router` and the `Cascade` row |
| Shared quota accounting across sessions | Requires two live stores and a shared quota source | Catalog scope: excluded, `limits`/`build` capability owns shared quota |
| Batch yielding and the batch gate under a real tool loop | The offline entry drives a single deterministic batch through the fixture | `m2-research-report-example` carries the multi-tool batch narrative; this entry proves one batch |
| `ClassContextOverflow` backstop (one compaction at the raised ratio, one retry, second overflow `Permanent`) | Requires a provider that reports `ClassContextOverflow` | `m2-context-calibration-overflow` (`openspec/specs/context/spec.md:111,125`); excluded here by catalog scope |

The example does not claim the adapter-owned half of the egress row, the exec-timeout leg or the context-overflow backstop.
