# ADR-0126: One error catalog — `ProblemOf` is the only client-facing mapping

Status: accepted · Origin: grill round 52 (2026-09-30)

## Decision

`messages` gains `ErrorCode`, `Problem` and `ProblemOf(err)`: a closed, versioned catalog mapping every sentinel and typed error to a stable code, `ErrorKind`, HTTP status and optional `RetryAfter`. `Detail` comes from fixed templates with allow-listed fields and never carries provider bodies, prompts, arguments or identities; unknown errors are `gohan.internal`. HTTP uses RFC 9457 problem details, streams send one terminal `error` event and treat a close without `done`/`error` as `gohan.stream_interrupted`, AG-UI and the MCP server carry the same code. `spec:types` fails when a spec declares an error without a catalog row.

## Context and evidence

RFC 9457 makes the stable `type` identifier the thing clients switch on and keeps human text separate; error-taxonomy guidance separates the class a client should act on from the cause. gohan had a rich Go-side error model and ad-hoc transport remarks, so each adapter would have mapped differently and provider text would have reached browsers.

## Consequences

`messages` v1.2 (types, catalog table, four scenarios), `streams` v1.4 (two scenarios), `agui` v1.4 (one scenario), generator coverage check; task 3; AG-UI scenario M4. The `errors.*` scenario prefix is registered under the `messages` capability because of the capability freeze.
