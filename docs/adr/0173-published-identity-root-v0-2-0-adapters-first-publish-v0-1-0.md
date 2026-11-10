# 0173: Published identity is root v0.2.0; adapters first publish at v0.1.0

## Status
Accepted (date 2026-11-10; owner ruling on release identity).

## Context
Root `v0.2.0` is published (tag → 3baeb4fe0e36655238c1c293ad9084cb22893edc; confirmed on origin). The local `v1.0.0` tag set — root plus all six adapters, all cut at 00075cd6eecaa7421abc851b95015ea626af84ec — was never pushed and is retired as unpublished drafts. Adapters pin root `v0.2.0` (commit 7069d1f). `adapter/otel/v0.1.0` is the only published adapter tag (→ 4ce59f7dd9b1df3b0a0516e3058912514478b0b7, same peeled commit as root `v0.1.0`) and declares root `v0.0.0` with a workspace `replace ../..`.

## Decision
1. Published root identity is `v0.2.0`; the draft `v1.0.0` line is superseded and retired.
2. Adapters' first-publication identity is `v0.1.0` against root `v0.2.0`; `adapter/otel` next publishes `v0.1.1`.
3. The seven never-pushed local `v1.0.0` draft tags are deleted locally, preserving the record here.
4. For publication identity this ADR supersedes the m2-api-freeze per-module tag plan; that plan's table and observed transcript remain the historical record.

## Record of retired draft tags (never pushed; all peel to 00075cd6eecaa7421abc851b95015ea626af84ec)
`v1.0.0`, `adapter/anthropic/v1.0.0`, `adapter/docker/v1.0.0`, `adapter/eino/v1.0.0`, `adapter/openai/v1.0.0`, `adapter/otel/v1.0.0`, `adapter/postgres/v1.0.0`.

## Consequences
- ADR 0173 supersedes the m2-api-freeze per-module `v1.0.0` tag plan (docs/design/compatibility.md:70-83) for publication identity only; that table and the observed post-freeze `api:check` transcript (docs/design/compatibility.md:99-121) stand as the historical record of the draft state.
- Adapters publish for the first time at `v0.1.0` pinning root `v0.2.0`; `adapter/otel` is already published at `v0.1.0` and its next release is `v0.1.1`.
- Root `v0.2.0` was published by owner ruling (a driver act at 3baeb4fe0e36655238c1c293ad9084cb22893edc); the repair-release-verification change's machinery publishes nothing.
- The seven local draft tags are deleted locally (`git tag -d`); no remote tag is touched and no history rewrite occurs.
