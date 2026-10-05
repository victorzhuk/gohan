# camunda-invoice

An offline invoice job composed from gohan primitives instead of a workflow
server: the external work item is fetched from a fixture queue, the run
claims a lease against memory stores, and a user task suspends the run for
human approval. The resume path re-checks live state before any side effect
runs — control freshness, the control-wait bound, the live kill flag, then
the originator's current scopes.

What it demonstrates:

- `engines.duplicate-workers` — two workers claim the same `OperationID`;
  the store lets exactly one through, the other sees `OperationExistsError`.
- `engines.suspended-runs-are-not-reclaimed` — a run suspended three days
  for approval never shows up in `Stale`, and `Reclaim` refuses it.
- `engines.revoked-authority-on-resume` — the originator's scope revoked
  while the run was suspended denies the pending call on resume.
- `engines.stale-control-state` — an unreachable flags provider suspends
  the effect on control state; past `MaxControlWait` the effect is denied.
- live deny beats approval — a kill flag switched on after the approval
  still wins: the tool never runs and the audit trail reads
  `approved`, then `flag_denied`.

## Run

```sh
go -C examples run ./camunda-invoice
go -C examples test -short -timeout 2m ./camunda-invoice/
```

No network, no API keys: the job queue, flags provider and clock are all
in-process fixtures.
