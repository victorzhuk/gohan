# Proposal

## Why

Provider argument streams must become complete, separately identified calls before governed execution.
Iterator termination and missing optional usage fields must not produce later yields or panics.

## What Changes

- Complete Anthropic argument blocks independently by content-block index.
- Emit identity-bearing previews and complete executable calls.
- Preserve forced-schema text mapping and mark unfinished arguments as truncated.
- Make OpenAI consumer stop and terminal error final, with body release on every exit.
- Treat absent cache details as zero cache tokens.
- Install the existing Eino call-ID handler through the shipped constructor.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `model`: complete provider calls, preview identity, final termination and optional usage.
- `runtime`: foreign constructor call identity at the governed boundary.

## Impact

This child owns parent tasks 6.1-6.5.
It changes the existing Anthropic, OpenAI and Eino adapters, controlled fixtures, conformance evidence and existing adapter/testing docs.
No dependency upgrade or new runtime abstraction is required.

## Acceptance

Controlled HTTP fixtures reach public `Build` and native conversations for parallel, completed, forced-schema and unfinished blocks.
The public foreign constructor carries Eino IDs through gate, approval, accounting and journal execution exactly once.
Early consumer stop and terminal errors close the body without later usage yields.
All three affected adapter conformance suites pass within the design bounds.

## Boundaries

Integrate authority and recovery outcomes first.
Preserve their call identity, accounting, terminal delivery and scenario ownership.
Independent declared-dependency release checks remain with the release child.
Do not alter the independently edited research-report change.
