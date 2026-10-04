# ADR-0055: `ModelProfile.QuotaPool` shares one limiter across every service on the same provider account; admission control by latency class; `Budget` scopes `run`, `tenant`, `flow`, `pool/day`; cost-anomaly signal

Status: accepted · Origin: gohan-spec v0.13 decision D55

## Decision

`ModelProfile.QuotaPool` shares one limiter across every service on the same provider account; admission control by latency class; `Budget` scopes `run`, `tenant`, `flow`, `pool/day`; cost-anomaly signal.

## Context and evidence

Provider limits are per organization account; one unpaced batch job starves interactive traffic.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
