package types

import "time"

// RunLimits bounds one flow's runs. A zero field takes its value from the
// installed preset, except ConsumerStall, MaxCost and MaxSandboxSeconds,
// where zero means unbounded.
type RunLimits struct {
	MaxTurns     int
	MaxToolCalls int
	MaxCost      float64
	MaxWallClock time.Duration
	SoftRatio    float64

	MaxDepth            int
	MaxParallelChildren int
	MaxParallelTools    int
	ConsumerStall       time.Duration
	MaxSandboxSeconds   float64
	MaxBlobBytes        int64
}

// The run-limit presets. Every preset sets its own MaxTurns and
// MaxToolCalls; fields they leave zero fall back to the defaults a
// preset may leave zero (MaxBlobBytes) or stay unbounded (MaxCost,
// MaxSandboxSeconds; ConsumerStall for Batch).
var (
	InteractiveLimits = RunLimits{
		ConsumerStall:       30 * time.Second,
		MaxTurns:            6,
		MaxToolCalls:        20,
		MaxWallClock:        60 * time.Second,
		SoftRatio:           0.8,
		MaxDepth:            2,
		MaxParallelChildren: 8,
		MaxParallelTools:    8,
	}
	AgenticLimits = RunLimits{
		ConsumerStall:       120 * time.Second,
		MaxTurns:            50,
		MaxToolCalls:        200,
		MaxWallClock:        30 * time.Minute,
		SoftRatio:           0.8,
		MaxDepth:            2,
		MaxParallelChildren: 8,
		MaxParallelTools:    8,
	}
	BatchLimits = RunLimits{
		MaxTurns:            20,
		MaxToolCalls:        100,
		MaxWallClock:        10 * time.Minute,
		SoftRatio:           0.9,
		MaxDepth:            1,
		MaxParallelChildren: 4,
		MaxParallelTools:    4,
	}
)
