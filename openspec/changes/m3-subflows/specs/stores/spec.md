# stores delta

## ADDED Requirements

### Requirement: Per-run cost recording

`Runs` SHALL expose an optional surface that records the spend a run charged before it finishes, so a sub-flow's own cost is readable from its run row. Core MUST discover it by type assertion and MUST NOT fail when a store lacks it: that run keeps `Cost` at zero and the tree total stays authoritative.

```go
type RunCostRecorder interface {
	RecordCost(ctx context.Context, l Lease, cost float64) error
}
```

#### Scenario: run cost recorded when the store supports it
ID: `stores.run-cost-optional`
- WHEN one run finishes on a store implementing `RunCostRecorder` and another on a store without it
- THEN the first run's row reports the recorded spend and the second row keeps zero without an error
