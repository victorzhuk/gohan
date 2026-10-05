# Changelog

All notable changes to the `github.com/victorzhuk/gohan` root module are documented here. Format: Keep a Changelog 1.1.0. Breaking changes while the module is `v0.x` are listed under *Breaking* (`docs/design/compatibility.md`).

## [Unreleased]

### Added

- Core types and ports declared **v1-candidate** (M0.5): the port method sets, handle shapes (`Conversation`, `Flow[In, Out]`), sentinel/typed error set and the optional-interface growth pattern are fixed in shape; the root module is not tagged `v1.0.0` until M2's exit criteria pass. See `docs/design/api-review-m0-5.md`.
- M0.5 API review published after the three offline acceptance processes (`examples/kafka-refunds`, `examples/temporal-travel`, `examples/camunda-invoice`) passed against the memory stores (ADR-0083, ADR-0136). The `api/gohan.yaml` / `adapter/httpapi` half of the `api:check` gate is recorded as a gap and lands in M4.
