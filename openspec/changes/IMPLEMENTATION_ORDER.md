# Implementation order

This order separates recorded API freeze from unfinished milestone acceptance. Existing uncommitted repair and research-report work retains its ownership.

| Order | Change | Completion dependency |
|---|---|---|
| 1 | repair-call-authority | Approved authority baseline |
| 2 | repair-token-recovery | Authority integrated |
| 3 | repair-provider-composition | Authority and recovery integrated |
| 4 | repair-sandbox-isolation | Preceding three repairs integrated |
| 5 | repair-network-pinning | Sandbox isolation integrated |
| 6 | repair-release-verification | All five repair outcomes integrated |
| 7 | telemetry-export-contracts | Release verification passes |
| 8 | telemetry-local-wal | Export contract accepted and implemented |
| 9 | production-online-evaluation | WAL and required release identity support available |
| 10 | m2-research-report-example, m2-data-analyst-example | Their composed public paths pass |
| 11 | m2 closure reconciliation | Example acceptance and milestone evidence complete |
| 12 | scoped m3, then remaining m4 | Predecessor acceptance reviewed |

repair-runtime-safety-contracts is the integration umbrella, not a seventh competing repair implementation. Serialize overlapping core/store/document edits.

m2-api-freeze records completed work and seven local v1.0.0 tags. Review it for archive eligibility without moving tags or claiming unfinished examples are complete. m2-e2b-sandbox remains independently deferred pending its documented external prerequisites.

production-online-evaluation owns only the minimum online slice pulled forward from M4. Full shadow orchestration and other release scope remain with their milestone owners. New package scenarios remain proposed until implementation admission registers them. No active coverage claim follows from authoring these packages.
