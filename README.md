# gohan

A Go library for building AI agent harnesses that survive production: fixed structure and governance (identity, permissions, exactly-once tool effects, suspension and resume, budgets, telemetry) around loops executed by adapters — native, [eino](https://github.com/cloudwego/eino), [adk-go](https://github.com/google/adk-go) — with providers, stores and protocols as separate modules.

Status: specification complete, implementation starts with `openspec/changes/m0-core`. Go 1.27, Apache-2.0.

## Quickstart

See `docs/design/scenarios.md` §9.0 — classify a support ticket in one typed `Extract` call with memory stores and no configuration beyond a model profile.

## Where to read

- `docs/overview.md` — purpose, principles, capability map, milestones
- `docs/design/architecture.md` — layering, core budget rule, repository layout
- `openspec/specs/<capability>/spec.md` — the contracts, with scenario IDs that are also test names
- `docs/adr/` — every decision and its evidence
- `AGENTS.md` — how to work in this repository (humans and agents alike)

## Modules

`github.com/victorzhuk/gohan` (core, std, testkit) · `adapter/{eino,adkgo,openai,anthropic,jev,cel,lispico,postgres,redis,mcp,agui,docker,e2b,langfuse,openfeature}` · `examples/`
