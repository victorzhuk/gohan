# Harness performance budgets

Capability: `performance` · Spec v1.0 baseline (restructured from gohan-spec v0.13; later decisions live in `docs/adr/`) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `performance` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### Budgets

The reference machine is the CI runner class (`ubuntu-latest`, 4 vCPU, amd64) on which baselines are recorded and the gate runs; local runs are advisory and never fail a PR. Numbers are baselined in M0 on the reference machine and then frozen as regression gates; they apply to `native` runtime with memory stores, no guards, unless stated.

| Path | Budget (initial) |
|---|---|
| taint match per tool call over the `MaxWindowBytes` window (default 256 KiB), 8 string args | ≤ 50 µs, index build ≤ 2 ms per turn |
| Chain overhead per `ReadOnly` tool call over the raw call | ≤ 20 µs, ≤ 8 allocations |
| Chain overhead per model call, excluding I/O | ≤ 50 µs |
| Assembler prefix build for an unchanged prefix | 0 allocations |
| `Message` ↔ eino `AgenticMessage` / adk-go `genai.Content` conversion | ≤ 1 allocation per block |
| `Explain` on a `std.Interactive()` flow | ≤ 5 ms |

Gate: benchmarks run on every PR with alternating base/head measurement, 3 rounds, fastest round, 5 % tolerance; `testing.AllocsPerRun` contracts on the paths above fail the build on any increase. On the reference runner the gate also enforces the table's absolute budgets as deltas over a matched raw measurement (tool overhead = fastest chain minus fastest raw tool call; model overhead against the raw model call) and fails when a configured benchmark or its raw comparator is missing, empty or malformed on either side — a missing measurement is never a pass. Local runs stay advisory and enforce no latency budget. A verdict is cached by a digest of the full measurement identity (cache schema version, both full commit SHAs, benchmark selector, benchtime, timeout and rounds, tolerance and latency-enforcement mode, the validated baseline configuration, toolchain and runner identity); configuration is validated before the cache lookup and the identity is revalidated inside the cached verdict, so an advisory pass can never satisfy a strict invocation.


## Requirements

### Requirement: Performance budgets

#### Scenario: chain overhead within budget
ID: `performance.chain-overhead-within-budget`
- WHEN the `BenchmarkToolChain_ReadOnly` benchmark runs on the reference machine
- THEN overhead over the raw call is within the frozen budget and allocations do not exceed the contract

### Requirement: Performance budgets

#### Scenario: prefix build allocation free
ID: `performance.prefix-build-allocation-free`
- WHEN the same prefix is assembled twice
- THEN the second build performs zero allocations

### Requirement: Performance budgets

#### Scenario: regression gate
ID: `performance.regression-gate`
- WHEN a PR increases a gated benchmark by more than the tolerance after retries
- THEN CI fails naming the benchmark and the delta
