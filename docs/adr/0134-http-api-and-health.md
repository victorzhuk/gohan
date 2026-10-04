# ADR-0134: HTTP API from one OpenAPI document; readiness from `Stack.Health`

Status: accepted · Origin: grill round 60 (2026-09-30)

## Decision

`adapter/httpapi` serves the `Stack` over REST + SSE from `api/gohan.yaml` (OpenAPI 3.1) through an `ogen`-generated server whose handlers only translate; `task api:check` keeps document and server in step. Sessions, runs (stream, background, wait), events with `Last-Event-ID`, resume, cancel, fork, delete, feedback, takeover, hold, explain, invoke, erase and memory are the resources; `X-Gohan-Multitask: steer|interrupt` maps a mid-run message to `Steer` or `Cancel`+`Send`; errors are `ProblemOf`. An `Authenticator` is mandatory and sets the principal as transport code. `Stack.Health` runs store pings, schema skew, platform-key validity and shutdown state into a `HealthReport`; `/readyz` follows `Ready`, `/healthz` only the process.

## Context and evidence

Agent servers expose one REST + SSE surface over threads and runs — create with stream/background/wait, reconnect by last event id, join, cancel with an action, a multitask strategy for input during a run, optional webhook per run — described by an OpenAPI document from which SDKs are generated. gohan had AG-UI for browsers and MCP for agents but nothing for services, mobile apps or ops tooling, and no readiness signal for a pod with a broken store or schema.

## Consequences

`interop` v1.3 (`Authenticator`, `NewHandler`, resource list, four scenarios), `runtime` v1.5 (`Health`, `HealthReport`, `Check`, rule block, two scenarios), `identity` v1.7 (one scenario), `build` v1.6 (`WithHealthTimeout`), layout `adapter/httpapi`, compatibility gate; task 27a; the adapter ships in M4 with AG-UI.
