# ADR-0130: Provider keys per request — tenant-bound source, fail closed, isolation per key

Status: accepted · Origin: grill round 56 (2026-09-30)

## Decision

Adapters fetch the provider credential per call from a `ProviderKeySource` with the tenant taken from the principal in `ctx`. `ModelProfile.Keys` selects `PlatformKey`, `TenantKey` (fail closed with `ErrNoProviderKey`) or `TenantOrPlatformKey` (fallback only when no tenant key exists, never on a provider auth error). Breaker, limiter, in-flight, quota and hedge state are keyed by `(profile, key id)`. The token never reaches persistence, telemetry, audit, `Explain`, cassettes or error details; the opaque key id does, and `Usage.KeyID` supports billing attribution. `Build` validates platform keys through the optional `ProviderKeyValidator`.

## Context and evidence

BYOK guidance: secrets in a dedicated layer only, tenant bound before the key is fetched, no silent substitution, provider quotas belonging to the tenant's account, usage attributed by tenant/provider/model, keys validated, rotated and revoked with execution-time checks. gohan's adapters held one process-wide key, so multi-tenant BYOK would have meant one stack per tenant or an adapter hack.

## Consequences

`model` v1.5 (`KeyMode`, `ProviderCredential`, `ProviderKeySource`, `ProviderKeyValidator`, `ErrNoProviderKey`, `Usage.KeyID`, rule block, six scenarios), `identity` v1.3 (one scenario), catalog rows, layout `std/keys`; task 18; isolation and telemetry scenarios M1.
