// Package flags ships the static/env flags provider, the frozen-snapshot
// evaluation, the live-control seam values and the freshness watcher. It
// ships no remote store and no OpenFeature adapter.
package flags

import (
	"context"
	"fmt"
	"iter"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
)

// DefaultFreshnessLimit bounds how old a live evaluation may be before it
// counts as stale.
const DefaultFreshnessLimit = 30 * time.Second

// DefaultMaxControlWait bounds the AwaitingControl suspension when no
// fresh state arrives.
const DefaultMaxControlWait = 5 * time.Minute

// EnvPrefix names the environment variables the provider reads above its
// static values: GOHAN_FLAG_<KEY>, parsed as the declared default's type.
const EnvPrefix = "GOHAN_FLAG_"

// Provider answers flags from static values, with environment overrides,
// and reports FromDefault when neither is set.
type Provider struct {
	values map[string]any
}

// NewProvider builds a provider over static values.
func NewProvider(values map[string]any) *Provider {
	v := make(map[string]any, len(values))
	for k, val := range values {
		v[k] = val
	}
	return &Provider{values: v}
}

func (p *Provider) Bool(ctx context.Context, key string, def bool, fc types.FlagContext) (bool, types.Freshness, error) {
	if raw, ok := os.LookupEnv(EnvPrefix + strings.ToUpper(key)); ok {
		v, err := strconv.ParseBool(raw)
		return v, freshNow(), err
	}
	if v, ok := p.values[key].(bool); ok {
		return v, freshNow(), nil
	}
	return def, defaulted(), nil
}

func (p *Provider) String(ctx context.Context, key string, def string, fc types.FlagContext) (string, types.Freshness, error) {
	if raw, ok := os.LookupEnv(EnvPrefix + strings.ToUpper(key)); ok {
		return raw, freshNow(), nil
	}
	if v, ok := p.values[key].(string); ok {
		return v, freshNow(), nil
	}
	return def, defaulted(), nil
}

func (p *Provider) Int(ctx context.Context, key string, def int64, fc types.FlagContext) (int64, types.Freshness, error) {
	if raw, ok := os.LookupEnv(EnvPrefix + strings.ToUpper(key)); ok {
		v, err := strconv.ParseInt(raw, 10, 64)
		return v, freshNow(), err
	}
	switch v := p.values[key].(type) {
	case int64:
		return v, freshNow(), nil
	case int:
		return int64(v), freshNow(), nil
	}
	return def, defaulted(), nil
}

func freshNow() types.Freshness {
	return types.Freshness{EvaluatedAt: time.Now().UTC()}
}

func defaulted() types.Freshness {
	return types.Freshness{EvaluatedAt: time.Now().UTC(), FromDefault: true}
}

// Control is the live-class evaluator: it re-evaluates its kill-switch
// keys before every protected effect and can only deny, force Ask or hold
// the effect for fresh state.
type Control struct {
	Flags types.Flags
	FC    types.FlagContext
	Keys  []string
	// FreshnessLimit marks an evaluation older than the limit stale.
	FreshnessLimit time.Duration
	// MaxControlWait bounds how long one effect holds for fresh state
	// before the next evaluation denies it and the run continues.
	MaxControlWait time.Duration
	// Now replaces the wall clock in tests.
	Now func() time.Time

	mu      sync.Mutex
	staleAt map[string]time.Time
}

func (c *Control) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}

// Check is the gate's live-control check.
func (c *Control) Check(ctx context.Context, inv *permission.ToolInvocation) (types.TaintAction, string) {
	action, reason, _ := c.evaluate(ctx)
	return action, reason
}

func (c *Control) evaluate(ctx context.Context) (types.TaintAction, string, bool) {
	limit := c.FreshnessLimit
	if limit <= 0 {
		limit = DefaultFreshnessLimit
	}
	wait := c.MaxControlWait
	if wait <= 0 {
		wait = DefaultMaxControlWait
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range c.Keys {
		v, fr, err := c.Flags.Bool(ctx, key, false, c.FC)
		if err != nil {
			return types.TaintDeny, fmt.Sprintf("flag %s unavailable: %v", key, err), false
		}
		stale := fr.FromDefault || c.now().Sub(fr.EvaluatedAt) > limit
		if !stale {
			delete(c.staleAt, key)
			if v {
				return types.TaintDeny, "disabled by kill switch " + key, false
			}
			continue
		}
		if c.staleAt == nil {
			c.staleAt = make(map[string]time.Time)
		}
		first, seen := c.staleAt[key]
		if !seen {
			c.staleAt[key] = c.now()
			return types.TaintAsk, permission.ControlWaitReason, true
		}
		if c.now().Sub(first) > wait {
			return types.TaintDeny, fmt.Sprintf("no fresh state for %s within the control wait", key), false
		}
		return types.TaintAsk, permission.ControlWaitReason, true
	}
	return types.TaintAllow, "", false
}

// Fresh reports whether every kill-switch key currently evaluates fresh
// and unset, the state the watcher waits for.
func (c *Control) Fresh(ctx context.Context) bool {
	action, _, _ := c.evaluate(ctx)
	return action == types.TaintAllow
}

// GateCheck installs c as the gate's live-control check.
func GateCheck(c *Control) permission.Option {
	return permission.WithLiveControl(c.Check)
}

// ModelStep is the model-chain step re-evaluating live flags before every
// model call. A default or stale evaluation suspends the pending call with
// AwaitingControl through the same control wait the gate holds.
func ModelStep(c *Control) chains.Step[types.ModelMiddleware] {
	return chains.Step[types.ModelMiddleware]{
		Name: "flags",
		Kind: chains.KindUser,
		Use: func(next types.ModelFunc) types.ModelFunc {
			return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
				return func(yield func(types.ModelChunk, error) bool) {
					action, reason, waiting := c.evaluate(ctx)
					switch {
					case action == types.TaintDeny:
						yield(types.ModelChunk{}, fmt.Errorf("gohan: model call blocked by flag: %s", reason))
					case waiting:
						wait := c.MaxControlWait
						if wait <= 0 {
							wait = DefaultMaxControlWait
						}
						yield(types.ModelChunk{}, &types.SuspendError{
							Reason: types.AwaitingControl,
							WakeAt: c.now().Add(wait),
						})
					default:
						next(ctx, req)(func(ch types.ModelChunk, err error) bool {
							return yield(ch, err)
						})
					}
				}
			}
		},
	}
}

// Watcher polls a control until fresh state arrives, then resumes the
// suspended run with the empty wake the AwaitingControl reason accepts.
type Watcher struct {
	Control *Control
	// Interval paces the poll; the default is one second.
	Interval time.Duration
	// Resume delivers the empty wake. It runs once, on fresh state.
	Resume func(ctx context.Context) error
}

// Run polls until fresh state or the context ends.
func (w Watcher) Run(ctx context.Context) error {
	interval := w.Interval
	if interval <= 0 {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if w.Control.Fresh(ctx) {
			return w.Resume(ctx)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
