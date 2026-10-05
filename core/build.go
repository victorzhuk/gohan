package gohan

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

var (
	// ErrNotPositive rejects a bound that must be at least one.
	ErrNotPositive = errors.New("gohan: must be positive")
	// ErrStrategyUnsupported names a strategy the profile cannot serve.
	ErrStrategyUnsupported = errors.New("gohan: strategy unsupported by profile")
	// ErrProviderToolUnsupported names a provider-executed tool the
	// profile's capabilities do not list.
	ErrProviderToolUnsupported = errors.New("gohan: provider tool unsupported")
	// ErrFallbackIncompatible names a fallback profile missing a
	// capability the flow can reach it with.
	ErrFallbackIncompatible = errors.New("gohan: fallback profile incompatible")
	// ErrFallbackUnknown names a fallback profile absent from the
	// configured set.
	ErrFallbackUnknown = errors.New("gohan: fallback profile not configured")
)

// StructuredStrategy selects how a flow's typed output is produced.
type StructuredStrategy int

const (
	// StructuredAuto resolves from the profile's capabilities.
	StructuredAuto StructuredStrategy = iota
	// StructuredConstrained uses the provider's constrained decoding.
	StructuredConstrained
	// StructuredToolSchema asks for the schema through a tool call and
	// validates app-side.
	StructuredToolSchema
)

// FlowRequest is what one flow declares to the strategy resolver at build
// time: an explicit strategy wins over the profile, and the profile's
// capabilities decide the default.
type FlowRequest struct {
	Name       string
	Structured bool
	Strategy   StructuredStrategy
	Tools      []types.ToolSpec
	Fallback   string
	// Blocks are the block kinds the flow can emit beyond the always
	// preserved text/tool kinds; the fidelity gate checks each one.
	Blocks []types.BlockKind
	// AllowDrop are kinds the flow accepts losing when a profile
	// declares them Dropped.
	AllowDrop []types.BlockKind
	// ProviderCompact lets the flow run adapter-side compaction.
	ProviderCompact bool
}

// StrategyPlan is the resolution the flow constructor runs with.
type StrategyPlan struct {
	Structured       StructuredStrategy
	ParallelTools    bool
	MaxParallelTools int
}

// Stack is the configured runtime Build returns.
type Stack struct {
	logger           *slog.Logger
	telemetry        types.Telemetry
	models           []types.Model
	middleware       []types.ModelMiddleware
	estimator        types.TokenEstimator
	keys             types.ProviderKeySource
	credentials      types.CredentialSource
	pinned           *types.PinnedManifest
	prompts          chains.PromptSet
	sequentialTools  bool
	maxParallelTools int
	limits           map[string]types.RunLimits
	stores           stores.Stores
	recovery         map[string]runtime.Runtime
	manifest         ReleaseManifest
}

// Limits returns the resolved run limits installed for a flow.
func (s *Stack) Limits(flow string) (types.RunLimits, bool) {
	l, ok := s.limits[flow]
	return l, ok
}

// Manifest returns the release identity Build computed over the pinned
// profiles, prompts and tool hashes.
func (s *Stack) Manifest() ReleaseManifest { return s.manifest }

// Build applies the options and validates everything knowable before the
// first call: option bounds, and the strategy resolution each configured
// flow resolves to. Flows validate further in their constructors.
func Build(opts ...Option) (*Stack, error) {
	cfg := config{logger: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}
	limits := make(map[string]types.RunLimits, len(cfg.limits))
	for flow, l := range cfg.limits {
		resolved, err := resolveLimits(flow, l)
		if err != nil {
			return nil, err
		}
		limits[flow] = resolved
	}
	profiles := make(map[string]types.ModelProfile, len(cfg.models))
	for _, m := range cfg.models {
		p := m.Profile()
		profiles[p.Name] = p
	}
	s := &Stack{
		logger:           cfg.logger,
		telemetry:        cfg.telemetry,
		models:           cfg.models,
		middleware:       cfg.middleware,
		estimator:        cfg.estimator,
		keys:             cfg.keys,
		credentials:      cfg.credentials,
		pinned:           cfg.pinned,
		prompts:          cfg.prompts,
		sequentialTools:  cfg.sequentialTools,
		maxParallelTools: cfg.maxParallelTools,
		limits:           limits,
		recovery:         cfg.recovery,
		stores:           cfg.stores,
	}
	s.manifest = computeReleaseManifest(profiles, cfg.prompts, cfg.pinned)
	logger := cfg.logger
	if logger == nil {
		logger = slog.Default()
	}
	// build.resolved-matrix: one structured record per build, never one
	// per profile. Flows resolve further via ResolveStrategies.
	logResolvedMatrix(logger, nil)
	return s, nil
}

// ResolveStrategies resolves one flow's strategies against its primary
// profile and, when the flow names one, its fallback: the flow's explicit
// request wins, then the profile, then the default derived from Caps.
func (s *Stack) ResolveStrategies(req FlowRequest, profiles map[string]types.ModelProfile, primary string) (StrategyPlan, error) {
	p, ok := profiles[primary]
	if !ok {
		return StrategyPlan{}, fmt.Errorf("flow %q: profile %q not configured", req.Name, primary)
	}
	plan := StrategyPlan{
		ParallelTools:    p.Caps.ParallelTools && !s.sequentialTools,
		MaxParallelTools: s.maxParallelTools,
	}
	if s.sequentialTools {
		plan.MaxParallelTools = 1
	}
	switch req.Strategy {
	case StructuredAuto:
		if req.Structured {
			if p.Caps.Constrained {
				plan.Structured = StructuredConstrained
			} else {
				plan.Structured = StructuredToolSchema
			}
		}
	case StructuredConstrained:
		if !p.Caps.Constrained {
			return plan, fmt.Errorf("flow %q: profile %q: strategy %q: %w",
				req.Name, p.Name, "constrained", ErrStrategyUnsupported)
		}
		plan.Structured = StructuredConstrained
	case StructuredToolSchema:
		plan.Structured = StructuredToolSchema
	}
	for _, spec := range req.Tools {
		if spec.Executor != types.ByProvider {
			continue
		}
		if _, ok := p.Caps.ProviderTools[spec.Name]; !ok {
			return plan, fmt.Errorf("flow %q: profile %q: provider tool %q: %w",
				req.Name, p.Name, spec.Name, ErrProviderToolUnsupported)
		}
	}
	if req.Fallback == "" {
		return plan, nil
	}
	fb, ok := profiles[req.Fallback]
	if !ok {
		return plan, fmt.Errorf("flow %q: fallback profile %q: %w", req.Name, req.Fallback, ErrFallbackUnknown)
	}
	if cap := fallbackGap(req, plan, fb); cap != "" {
		return plan, fmt.Errorf("flow %q: fallback profile %q: capability %q: %w",
			req.Name, req.Fallback, cap, ErrFallbackIncompatible)
	}
	return plan, nil
}

// fallbackGap returns the first capability the fallback lacks that the flow
// can reach it with: tools, then constrained output.
func fallbackGap(req FlowRequest, plan StrategyPlan, fb types.ModelProfile) string {
	if len(req.Tools) > 0 && !fb.Caps.Tools {
		return "tools"
	}
	if plan.Structured == StructuredConstrained && !fb.Caps.Constrained {
		return "constrained"
	}
	return ""
}
