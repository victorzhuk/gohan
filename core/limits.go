package gohan

import (
	"fmt"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// Defaults for the fields a preset may leave zero. MaxCost, MaxSandboxSeconds
// and ConsumerStall have no default: zero stays unbounded.
const (
	defaultMaxTurns     = 20
	defaultMaxToolCalls = 50
	defaultMaxWallClock = 10 * time.Minute
	defaultSoftRatio    = 0.8
	defaultMaxBlobBytes = 256 << 20
)

// WithLimits installs the run limits for one flow. A zero field takes its
// value from the installed preset; a set that leaves a required bound at
// zero and no preset to take it from fails Build, because unbounded runs
// are never implicit.
func WithLimits(flow string, limits types.RunLimits) Option {
	return func(c *config) error {
		if c.limits == nil {
			c.limits = make(map[string]types.RunLimits)
		}
		c.limits[flow] = limits
		return nil
	}
}

// resolveLimits refuses an unbounded set and applies the defaults the
// spec gives the fields a preset leaves zero.
func resolveLimits(flow string, l types.RunLimits) (types.RunLimits, error) {
	if l.MaxTurns <= 0 || l.MaxToolCalls <= 0 || l.MaxWallClock <= 0 ||
		l.SoftRatio <= 0 || l.SoftRatio > 1 ||
		l.MaxDepth <= 0 || l.MaxParallelChildren <= 0 || l.MaxParallelTools <= 0 {
		return types.RunLimits{}, fmt.Errorf("gohan: flow %q: unbounded run limits", flow)
	}
	if l.MaxBlobBytes == 0 {
		l.MaxBlobBytes = defaultMaxBlobBytes
	}
	return l, nil
}
