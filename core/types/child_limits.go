package types

import "time"

// ChildLimits bounds one sub-flow call. FixedCost is the cap ShareFixed
// applies; ShareRemaining ignores it.
type ChildLimits struct {
	MaxWallClock time.Duration
	CostShare    CostShare
	FixedCost    float64
}

// CostShare selects how a child's cost budget derives from the tree.
type CostShare int

const (
	ShareRemaining CostShare = iota
	ShareFixed
)

// OnError selects a fan-out wave's failure policy.
type OnError int

const (
	FailFast OnError = iota
	Collect
)
