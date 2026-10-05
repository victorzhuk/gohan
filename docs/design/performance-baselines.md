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

The gate checks two different latency contracts on the reference machine.
The absolute budgets are:

- tool-chain overhead per read-only call: ≤ 20 µs
- model-chain overhead per call excluding I/O: ≤ 50 µs

The gate computes each overhead as the fastest head round of the chain
benchmark minus the fastest head round of its raw comparator
(`BenchmarkToolChain_ReadOnly` − `BenchmarkToolCall_Raw`,
`BenchmarkModelChain` − `BenchmarkModelCall_Raw`) and holds it against the
frozen budget from the JSON. Strict mode (`--latency`) enforces the budgets
and the relative regression; a local run reports a budget miss as advisory
and never fails.

The `ubuntu-latest` CI runner class (4 vCPU, amd64) supplies reference
measurements. The `performance.yml` workflow runs the gate in strict mode on
that class. The gate checks relative regressions separately: it compares the
fastest of three alternating base/head rounds and allows 5% regression.

Local latency is advisory. The local run does not enforce latency budgets.
`TestChainPerformanceBudget` enforces allocation counts locally and reports
tool-chain timing as advisory data. It does not prove reference-runner
latency. An advisory PASS never satisfies a strict run: the latency mode is
part of the verdict cache identity, so a strict run of the same commit pair
re-measures.

## Verdict cache identity

The gate caches verdicts under a versioned identity (`schema2`): full commit
SHAs, benchmark selector, effective benchtime, inner test timeout, round
count (3), tolerance, latency mode, the SHA256 digest of the validated
baselines file, Go version, GOOS/GOARCH/GOMAXPROCS, and runner identity. A
cache entry is reused only when every field matches; entries from earlier
schemas are ignored. The CI cache key in `.github/workflows/performance.yml`
encodes the schema, toolchain (`go.mod`), baseline digest, runner class, and
the full base/head commit pair. The baselines file is loaded and validated
before the cache is consulted: the gated set must be nonempty and both
latency budgets must be positive.

## Required measurements

The `gated` list in the JSON names required benchmarks, including raw
comparators. The performance gate must fail when any required benchmark lacks
a nonempty sample on either side, when fewer than three rounds produced
samples, or when a round reports a zero ns/op sample; the failure names the
benchmark.

Re-freezing: run

```
go test -timeout 3m -run '^$' -bench 'BenchmarkToolChain_ReadOnly|BenchmarkToolCall_Raw|BenchmarkModelChain|BenchmarkModelCall_Raw' -benchtime 100000x ./core/
```

Update the allocation counts in `core/performance_baselines.json`. Keep the
ratio class label accurate.

