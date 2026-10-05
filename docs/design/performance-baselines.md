# Performance baselines

`core/performance_baselines.json` freezes the machine-independent part of the
`performance` capability contract: allocation counts and chain-overhead
ratios. `core/chain_benchmark_test.go` embeds that file and enforces the
allocation counts with `testing.AllocsPerRun`, so any increase on the gated
paths fails the build everywhere, on every machine.

## Machine-independent numbers

Allocation counts are deterministic for a given toolchain: `BenchmarkToolCall_Raw`
(0 allocs/op), `BenchmarkToolChain_ReadOnly` (7 allocs/op, all of them chain
overhead over the raw call) and `BenchmarkModelChain` (12 allocs/op) were
measured with `testing.AllocsPerRun` and frozen in the JSON. The spec's
contract is "chain overhead ≤ 8 allocations" per read-only tool call; the
frozen 7 leaves one allocation of headroom. The budget test reads these
fields from the file, so re-baselining is a one-file change.

The ratios in `measured_ratios` (tool chain ≈ 53x raw, model chain ≈ 67x raw)
were taken on a developer workstation (AMD Ryzen 5 9600X, linux/amd64). They
are recorded as advisory context only, with the machine class named in the
file, and nothing gates on them.

## Runner-class expectations

The absolute wall-clock budgets belong to the reference machine, the
`ubuntu-latest` CI runner class (4 vCPU, amd64) that `.github/workflows/ci.yml`
uses for its `bench` job and `Taskfile.yml` uses for `task bench`:

- tool-chain overhead per read-only call: ≤ 20 µs (spec: ≤ 20 µs)
- model-chain overhead per call excluding I/O: ≤ 50 µs (spec: ≤ 50 µs)

No developer-machine microsecond number is frozen anywhere. On this
workstation both paths run under 1 µs, far inside the runner-class budgets;
the local advisory check in `TestChainPerformanceBudget` logs — never fails —
when the tool-chain path drifts past 20 µs. The strict verdicts come from the
CI gate on the reference runner (chunk 32.3, alternating base/head rounds,
fastest of three, 5 % tolerance).

## What is gated

`gated` in the JSON lists the benchmarks the CI gate drives:
`BenchmarkToolChain_ReadOnly` and `BenchmarkModelChain`.
`BenchmarkToolCall_Raw` is the reference baseline both budgets are deltas
over, so it is measured but not itself a gate.

Re-freezing: run

```
go test -timeout 3m -run '^$' -bench 'BenchmarkToolChain_ReadOnly|BenchmarkToolCall_Raw|BenchmarkModelChain' -benchtime 100000x ./core/
```

update the allocation counts in `core/performance_baselines.json`, and keep
the ratio class label honest.
