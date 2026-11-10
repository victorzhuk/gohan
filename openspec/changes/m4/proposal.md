# Change: m4

Stub: scope only; tasks are written when the previous milestone's M-review closes.

## Scope

`skills` (`std/skills`, adapter/langfuse SkillSource), `agui` (adapter/agui), `interop` HTTP API (adapter/httpapi, `api/gohan.yaml`, ogen), evals module (`release`), `languages` (`core/flowdef` + `std/flow.Compile` + YAML front-end, adapter/cel, adapter/lispico), adapter/jev, adapter/openfeature, `catalog-enrichment`, `invoice-extraction`.

## Exit

The minimum online evaluation slice is now ordered before the remaining milestone work through `production-online-evaluation`. That change owns bounded sampling, calibration, unscored outcomes and durable result provenance. Full shadow orchestration, datasets and other evals scope remain here. See `openspec/changes/IMPLEMENTATION_ORDER.md`.

See the milestone table in `docs/overview.md`.
