package gohan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/permission"
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
	logger             *slog.Logger
	telemetry          types.Telemetry
	sandboxHandles     types.SandboxHandles
	sandboxPolicy      types.SandboxPolicy
	models             []types.Model
	middleware         []types.ModelMiddleware
	estimator          types.TokenEstimator
	contextGuard       guards.Guard
	compactionFallback stores.CompactionFallback
	keys               types.ProviderKeySource
	credentials        types.CredentialSource
	secrets            types.SecretResolver
	notifier           types.Notifier
	approvalPolicy     permission.ApprovalPolicySource
	approvalGrants     permission.GrantStore
	verifier           types.Verifier
	egressClient       func(p types.EgressPolicy) *http.Client
	pinned             *types.PinnedManifest
	prompts            chains.PromptSet
	sequentialTools    bool
	allowAnonymous     bool
	allowCache         bool
	maxParallelTools   int
	limits             map[string]types.RunLimits
	stores             stores.Stores
	tenant             types.Tenant
	native             map[string]*resolvedNativeConfig
	foreign            map[string]func() runtime.Runtime
	recovery           map[string]runtime.Runtime
	manifest           ReleaseManifest
	consolidator       func(ConsolidationWiring) MemoryConsolidator
	evaluator          RunEvaluator
	flags              types.Flags
	frozen             map[string]any
	releaseID          string
	variant            string
	variantFlag        string
	shadow             bool
	titleProfile       string
	controlWait        time.Duration
	streamBuffer       int
	latencyTimeouts    map[types.LatencyClass][3]time.Duration
}

// TitleProfile returns the profile the title recipe runs on: the option's
// choice, or the cheapest configured profile when unset.
func (s *Stack) TitleProfile() string { return s.titleProfile }

// cheapestProfile names the profile with the lowest uncached input and
// output price. A tie keeps the first name in map order's tie-break: the
// lexicographically smaller name.
func cheapestProfile(profiles map[string]types.ModelProfile) string {
	best := ""
	var bestCost float64
	for name, p := range profiles {
		cost := p.Pricing.Input + p.Pricing.Output
		if best == "" || cost < bestCost || (cost == bestCost && name < best) {
			best, bestCost = name, cost
		}
	}
	return best
}

// Secrets returns the resolver named sandbox secret values resolve from.
func (s *Stack) Secrets() types.SecretResolver { return s.secrets }

// Limits returns the resolved run limits installed for a flow.
func (s *Stack) Limits(flow string) (types.RunLimits, bool) {
	l, ok := s.limits[flow]
	return l, ok
}

// Notifier returns the sink WithNotifier bound for run notice delivery.
func (s *Stack) Notifier() types.Notifier { return s.notifier }

// Prompts returns the PromptSet Build resolved. Recipes read the caller's
// model-facing strings here instead of carrying a literal of their own.
func (s *Stack) Prompts() chains.PromptSet { return s.prompts }

// Manifest returns the release identity Build computed over the pinned
// profiles, prompts and tool hashes.
func (s *Stack) Manifest() ReleaseManifest { return s.manifest }

// Build applies the options and validates everything knowable before the
// first call: option bounds, and the strategy resolution each configured
// flow resolves to. Flows validate further in their constructors.
func Build(opts ...Option) (*Stack, error) {
	cfg := config{
		logger:          slog.New(slog.DiscardHandler),
		sunsetWindow:    DefaultSunsetNoticeWindow,
		controlWait:     permission.DefaultControlWait,
		streamBuffer:    DefaultStreamBuffer,
		latencyTimeouts: defaultTimeouts,
	}
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
	if err := checkSunsets(cfg.logger, cfg.telemetry, cfg.sunsetWindow, cfg.sunsetWarnOnly, profiles); err != nil {
		return nil, err
	}
	titleProfile := cfg.titleProfile
	if titleProfile == "" {
		titleProfile = cheapestProfile(profiles)
	}
	s := &Stack{
		logger:             cfg.logger,
		telemetry:          cfg.telemetry,
		models:             cfg.models,
		middleware:         cfg.middleware,
		estimator:          cfg.estimator,
		contextGuard:       cfg.contextGuard,
		compactionFallback: cfg.compactionFallback,
		keys:               cfg.keys,
		credentials:        cfg.credentials,
		secrets:            cfg.secrets,
		notifier:           cfg.notifier,
		approvalPolicy:     cfg.approvalPolicy,
		approvalGrants:     cfg.approvalGrants,
		verifier:           cfg.verifier,
		egressClient:       cfg.egressClient,
		pinned:             cfg.pinned,
		prompts:            cfg.prompts,
		sequentialTools:    cfg.sequentialTools,
		allowAnonymous:     cfg.allowAnonymous,
		allowCache:         cfg.allowCache,
		maxParallelTools:   cfg.maxParallelTools,
		limits:             limits,
		recovery:           cfg.recovery,
		stores:             cfg.stores,
		tenant:             cfg.tenant,
		consolidator:       cfg.consolidator,
		evaluator:          cfg.evaluator,
		flags:              cfg.flags,
		frozen:             cfg.frozen,
		releaseID:          cfg.releaseID,
		variant:            cfg.variant,
		variantFlag:        cfg.variantFlag,
		shadow:             cfg.shadow,
		titleProfile:       titleProfile,
		controlWait:        cfg.controlWait,
		streamBuffer:       cfg.streamBuffer,
		latencyTimeouts:    cfg.latencyTimeouts,
		sandboxHandles:     cfg.sandboxReg.Handles,
		sandboxPolicy:      cfg.sandboxReg.Policy,
	}
	if err := checkSandboxRegistration(cfg.sandboxReg, cfg.stores); err != nil {
		return nil, err
	}
	if err := checkResidency(cfg.tenant, cfg.stores, profiles); err != nil {
		return nil, err
	}
	if err := s.resolveNative(cfg.native, profiles, cfg.redactor, cfg.redactionPolicy); err != nil {
		return nil, err
	}
	s.foreign = make(map[string]func() runtime.Runtime, len(cfg.foreign))
	if err := s.resolveForeign(cfg.foreign, profiles, cfg.redactor, cfg.redactionPolicy); err != nil {
		return nil, err
	}
	s.manifest = computeReleaseManifest(profiles, cfg.prompts, cfg.pinned)
	logger := cfg.logger
	if logger == nil {
		logger = slog.Default()
	}
	warnHostExec(logger, cfg.native, cfg.foreign)
	// build.resolved-matrix: one structured record per build, never one
	// per profile. Flows resolve further via ResolveStrategies.
	logResolvedMatrix(logger, nativeMatrixEntries(s.native))
	return s, nil
}

// hostExecAcknowledger is the optional registration fact a host runner
// declares; core never names the runner package.
type hostExecAcknowledger interface{ HostExecAcknowledged() bool }

// warnHostExec records once per registered tool that runs on the host
// without the acknowledgement the runner option grants, native or foreign.
func warnHostExec(logger *slog.Logger, natives []NativeSpec, foreign []ForeignSpec) {
	seen := map[string]bool{}
	for _, def := range natives {
		warnHostExecTools(logger, def.Tools, seen)
	}
	for _, def := range foreign {
		warnHostExecTools(logger, def.Tools, seen)
	}
}

func warnHostExecTools(logger *slog.Logger, tools []types.Tool, seen map[string]bool) {
	for _, t := range tools {
		ack, ok := t.(hostExecAcknowledger)
		if !ok || ack.HostExecAcknowledged() {
			continue
		}
		name := t.Spec().Name
		if seen[name] {
			continue
		}
		seen[name] = true
		logger.Warn("tool runs on the host without isolation; register with AllowHostExec to acknowledge",
			"tool", name)
	}
}

// Tenant returns the tenant identity Build was bound to.
func (s *Stack) Tenant() types.Tenant { return s.tenant }

// SandboxHandles returns the registered sandbox coordinator, or nil when
// the build registered none.
func (s *Stack) SandboxHandles() types.SandboxHandles { return s.sandboxHandles }

// SandboxPolicy returns the policy the registered coordinator was built
// for.
func (s *Stack) SandboxPolicy() types.SandboxPolicy { return s.sandboxPolicy }

// checkSandboxRegistration fails a PerSession registration the build
// cannot run: no coordinator, or a session log without the durable
// workspace metadata surface. No tool set is returned either way.
func checkSandboxRegistration(reg SandboxRegistration, st stores.Stores) error {
	if reg.Policy.Lifecycle != types.PerSession {
		return nil
	}
	if reg.Handles == nil {
		return fmt.Errorf("sandbox registration: per-session policy declares no coordinator: %w", types.ErrPerSessionUnsupported)
	}
	if st.SessionLog == nil {
		return fmt.Errorf("sandbox registration: per-session policy declares no workspace metadata store: %w", types.ErrPerSessionUnsupported)
	}
	if _, ok := st.SessionLog.(stores.SessionWorkspaceMeta); !ok {
		return fmt.Errorf("sandbox registration: per-session policy declares no workspace metadata store: %w", types.ErrPerSessionUnsupported)
	}
	return nil
}

// checkResidency fails a residency-bound build against any store port or
// reachable model profile outside the tenant region. A store port without
// a region cannot prove where it keeps data, so it is refused too.
func checkResidency(t types.Tenant, st stores.Stores, profiles map[string]types.ModelProfile) error {
	if t.Residency == "" {
		return nil
	}
	ports := []struct {
		name string
		port any
	}{
		{"session_log", st.SessionLog},
		{"runs", st.Runs},
		{"checkpoints", st.Checkpoints},
		{"journal", st.Journal},
		{"redaction_maps", st.RedactionMaps},
	}
	for _, p := range ports {
		if p.port == nil {
			continue
		}
		rp, ok := p.port.(stores.RegionProvider)
		if !ok {
			return fmt.Errorf("residency: tenant %q: store %q: no region, want %q", t.ID, p.name, t.Residency)
		}
		if region := rp.Region(); region != t.Residency {
			return fmt.Errorf("residency: tenant %q: store %q region %q, want %q", t.ID, p.name, region, t.Residency)
		}
	}
	for _, p := range profiles {
		if p.Region != t.Residency {
			return fmt.Errorf("residency: tenant %q: profile %q region %q, want %q", t.ID, p.Name, p.Region, t.Residency)
		}
	}
	return nil
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
			if s.telemetry != nil {
				s.telemetry.Count(context.Background(), "gohan.tool.provider_unsupported", 1, types.String("tool", spec.Name))
			}
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
