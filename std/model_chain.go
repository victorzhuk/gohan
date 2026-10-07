package std

import (
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/std/hedge"
	"github.com/victorzhuk/gohan/std/route"
	"github.com/victorzhuk/gohan/std/telemetry"
)

// ModelChain assembles the recommended model chain around a router: the
// router outermost, the hedge inside it and the fallback innermost, the
// position the ordering rules require. The hedge reads the router's
// eligibility view for its targets, so a breaker-open or half-open endpoint
// never becomes a hedge target and no probe is consumed by looking, and its
// default After reads the rolling P90 the latency window records.
//
// When the router offers fewer than two eligible targets the step can never
// find a different endpoint, so the chain names the step inert and Explain
// carries the note in the step's name.
func ModelChain(r *route.Router, opts ...ChainOption) chains.ModelChain {
	cfg := chainConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	p := cfg.policy
	if p.Targets == nil {
		p.Targets = r.Eligible
	}
	var mopts []hedge.Option
	if cfg.latency != nil {
		mopts = append(mopts, hedge.WithLatency(func(endpoint string) time.Duration {
			d, _ := cfg.latency.P90(endpoint)
			return d
		}))
	}
	name := "hedge"
	if len(r.Eligible()) < 2 {
		name = "hedge (inert: single target)"
	}
	return chains.ModelChain{
		{Name: "router", Kind: chains.KindRouter},
		{Name: name, Kind: chains.KindHedge, Use: hedge.New(p, mopts...)},
		{Name: "fallback", Kind: chains.KindFallback},
	}
}

// WithHedgePolicy replaces the hedge step's policy. The targets supplier
// the policy carries wins over the router's eligibility view.
func WithHedgePolicy(p hedge.HedgePolicy) ChainOption {
	return func(c *chainConfig) { c.policy = p }
}

// WithLatencyWindow wires the rolling latency window the hedge default
// reads. A zero-value config leaves the step on its 800 ms floor.
func WithLatencyWindow(w *telemetry.LatencyWindow) ChainOption {
	return func(c *chainConfig) { c.latency = w }
}

type chainConfig struct {
	policy  hedge.HedgePolicy
	latency *telemetry.LatencyWindow
}

// ChainOption adjusts the model chain the builder assembles.
type ChainOption func(*chainConfig)
