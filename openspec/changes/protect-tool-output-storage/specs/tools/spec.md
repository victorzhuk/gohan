# Spec Delta

## Context

This delta ADDS one requirement to the `tools` capability. It does not restate or replace the broad `Egress` requirement or its scenarios, which stay with their current package. The ids below are proposed and unused by another package.

## ADDED Requirements

### Requirement: Fetched response cap and store

A tool that returns a fetched response MUST bound its inline content by `ToolSpec.MaxOutput` and MUST spill the rendered response to a wired `OutputStore`, returning a head excerpt beside a `Ref`. `std/tool/http.Fetch` MUST honour this. A response of at most `MaxOutput` bytes MAY stay whole inline with no `Ref`. A larger response MUST NOT be returned whole inline, and the head excerpt MUST be at most `MaxOutput` bytes plus the truncation marker.

#### Scenario: web_fetch overflow stored
ID: `tools.fetch-output-stored` (proposed)
- WHEN `web_fetch` receives a response larger than `MaxOutput` with an output store wired
- THEN the result carries a head excerpt plus a `Ref`, `read_output(ref)` returns the full rendered response, and `gohan.output.stored` increments

#### Scenario: web_fetch capped without a store
ID: `tools.fetch-output-no-store` (proposed)
- WHEN `web_fetch` receives a response larger than `MaxOutput` and no store is wired
- THEN the result carries a bounded head excerpt and a marker naming the dropped bytes and the missing store

### Requirement: Fetched rendered text includes the status line

The rendered response MUST be the HTTP status line followed by the body. The status prefix MUST count against `MaxOutput` and MUST be part of the stored document, so the prefix cannot push inline content past the cap.

#### Scenario: web_fetch call shape stays compatible
ID: `tools.fetch-options-compatible` (proposed)
- WHEN existing code calls `http.Fetch(policy)` with one argument and a response whose status line plus body fits `MaxOutput`
- THEN the tool returns the whole fetched rendered text with no `Ref`, and no store option is required
