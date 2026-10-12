package gohan

import (
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

// Option adjusts Build. Every option takes a floor type or interface: the
// driver never imports std, so the caller passes the std policy value.
type Option func(*config) error

// DefaultSunsetNoticeWindow is the compatibility mirror of the std policy
// value std.SunsetNoticeWindow: Build applies it when no
// WithSunsetNoticeWindow option is given, so an un-optioned caller keeps
// the packaged notice window. It must not be changed independently of
// std.SunsetNoticeWindow.
const DefaultSunsetNoticeWindow = 30 * 24 * time.Hour

// WithSunsetNoticeWindow sets how long before ModelProfile.Sunset a
// pinned profile must still be at Build. A profile inside the window
// fails the build unless WithSunsetWarnOnly is set. Core declares no
// window of its own: without this option no sunset gate runs, and std
// supplies the recommended window.
func WithSunsetNoticeWindow(d time.Duration) Option {
	return func(c *config) error {
		if d <= 0 {
			return fmt.Errorf("gohan: sunset notice window must be positive, got %s", d)
		}
		c.sunsetWindow = d
		return nil
	}
}

// WithSunsetWarnOnly downgrades the sunset notice-window failure to a
// warning: the build proceeds and emits gohan.model.sunset_warning.
func WithSunsetWarnOnly() Option {
	return func(c *config) error {
		c.sunsetWarnOnly = true
		return nil
	}
}

// WithControlWait sets how long the permission gate holds an
// AwaitingControl suspension before the pending call is denied and the run
// continues. Core declares no wait of its own: without this option the wake
// is armed at once, and std supplies the recommended wait.
func WithControlWait(d time.Duration) Option {
	return func(c *config) error {
		c.controlWait = d
		return nil
	}
}

// WithStreamBuffer sets the per-call chunk capacity and the conversation
// hand-off depth. Core declares no capacity of its own: without this option
// a call hands one chunk over at a time, and std supplies the recommended
// capacity.
func WithStreamBuffer(capacity int) Option {
	return func(c *config) error {
		c.streamBuffer = capacity
		return nil
	}
}

// WithLatencyTimeouts sets the per-class connect / first-chunk / idle table
// a profile's zero ModelTimeout falls back to. Core declares no table of its
// own: without this option a zero timeout stays a timeout of none, and std
// supplies the recommended table.
func WithLatencyTimeouts(t map[types.LatencyClass][3]time.Duration) Option {
	return func(c *config) error {
		c.latencyTimeouts = t
		return nil
	}
}

// SandboxRegistration carries the constructed sandbox coordinator and the
// policy it was built for into Build. The instance is shared by the tool
// set and the lifecycle hooks, so both callers hold one coordinator.
type SandboxRegistration struct {
	Handles types.SandboxHandles
	Policy  types.SandboxPolicy
}

// WithSandboxRegistration registers the sandbox coordinator on the build. A
// PerSession policy with no registration, or with a registration whose
// session log lacks the workspace metadata surface, fails Build.
func WithSandboxRegistration(reg SandboxRegistration) Option {
	return func(c *config) error {
		c.sandboxReg = reg
		return nil
	}
}

type config struct {
	flags              types.Flags
	frozen             map[string]any
	controlWait        time.Duration
	streamBuffer       int
	latencyTimeouts    map[types.LatencyClass][3]time.Duration
	foreign            []ForeignSpec
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
	logger             *slog.Logger
	telemetry          types.Telemetry
	pinned             *types.PinnedManifest
	prompts            chains.PromptSet
	sequentialTools    bool
	allowAnonymous     bool
	allowCache         bool
	maxParallelTools   int
	limits             map[string]types.RunLimits
	recovery           map[string]runtime.Runtime
	stores             stores.Stores
	redactor           types.Redactor
	redactionPolicy    types.RedactionPolicy
	tenant             types.Tenant
	native             []NativeSpec
	consolidator       func(ConsolidationWiring) MemoryConsolidator
	sunsetWindow       time.Duration
	sunsetWarnOnly     bool
	titleProfile       string
	sandboxReg         SandboxRegistration
}

// WithTenant binds the build to a tenant identity. A tenant with a
// Residency makes Build fail closed against stores and model profiles in
// other regions.
func WithTenant(t types.Tenant) Option {
	return func(c *config) error {
		c.tenant = t
		return nil
	}
}

// WithStores binds the store ports Recover runs against. A zero field
// means the operation that needs it is refused.
func WithStores(s stores.Stores) Option {
	return func(c *config) error {
		c.stores = s
		return nil
	}
}

// WithNotifier binds the sink std/notify delivers run notices to. The host
// runs the dispatcher; a Detached flow without one only ever reports
// through Attach, and the conversation logs a warning for it.
func WithNotifier(n types.Notifier) Option {
	return func(c *config) error {
		c.notifier = n
		return nil
	}
}

// WithRecoveryRuntime registers the backend runtime Recover re-drives a
// flow's runs with. A flow without one cannot be re-run headlessly; its
// stale runs finish as Failed with Uncertain.
func WithRecoveryRuntime(flow string, rt runtime.Runtime) Option {
	return func(c *config) error {
		if c.recovery == nil {
			c.recovery = make(map[string]runtime.Runtime)
		}
		c.recovery[flow] = rt
		return nil
	}
}

// WithPrompts sets the PromptSet whose strings the manifest hashes; core
// declares no default, so an absent set hashes as the empty set.
func WithPrompts(p chains.PromptSet) Option {
	return func(c *config) error {
		c.prompts = p
		return nil
	}
}

// WithModels registers the provider models the stack routes over.
func WithModels(models ...types.Model) Option {
	return func(c *config) error {
		c.models = append(c.models, models...)
		return nil
	}
}

// WithProviderKeys sets the credential source for platform-key profiles.
func WithProviderKeys(src types.ProviderKeySource) Option {
	return func(c *config) error {
		c.keys = src
		return nil
	}
}

// WithCredentialSource sets the source that re-issues the originator's
// credential when a suspended run resumes, before any tool executes
// (identity.credentials-on-resume). NewConversation picks it up from the
// stack unless a conversation option overrides it.
func WithCredentialSource(src types.CredentialSource) Option {
	return func(c *config) error {
		c.credentials = src
		return nil
	}
}

// WithSecretResolver sets the source named SecretRef values resolve from
// at workspace Open, for the run originator principal. A build without one
// injects nothing; a secret-named call fails as an ordinary tool error.
func WithSecretResolver(src types.SecretResolver) Option {
	return func(c *config) error {
		c.secrets = src
		return nil
	}
}

// WithApprovalPolicySource sets the source Conversation approval decisions
// resolve their policy from. The caller passes the floor interface value;
// std supplies the default implementation.
func WithApprovalPolicySource(src permission.ApprovalPolicySource) Option {
	return func(c *config) error {
		c.approvalPolicy = src
		return nil
	}
}

// ApprovalGrants binds the store session grants from ApproveScope are
// written to and read from. The caller passes the floor interface value;
// std supplies the in-memory implementation. A build without one refuses
// a scoped approval rather than granting without a stored grant.
func ApprovalGrants(store permission.GrantStore) Option {
	return func(c *config) error {
		c.approvalGrants = store
		return nil
	}
}

// WithEgressClient sets the HTTP client factory for tool egress policies.
func WithEgressClient(fn func(p types.EgressPolicy) *http.Client) Option {
	return func(c *config) error {
		c.egressClient = fn
		return nil
	}
}

// WithModelMiddleware wraps every model invocation. The first middleware is
// the outermost.
func WithModelMiddleware(mw ...types.ModelMiddleware) Option {
	return func(c *config) error {
		c.middleware = append(c.middleware, mw...)
		return nil
	}
}

// WithEstimator sets the token estimator the context budget consults.
func WithEstimator(est types.TokenEstimator) Option {
	return func(c *config) error {
		c.estimator = est
		return nil
	}
}

// WithCompactionFallback sets the projection Build applies when a guard
// rejects a composed compaction summary. A flow whose policy carries a
// compactor requires a fallback that validates against the flow's profile.
func WithCompactionFallback(p stores.CompactionFallback) Option {
	return func(c *config) error {
		c.compactionFallback = p
		return nil
	}
}

// WithCompactionGuard sets the context guard the compaction stage consults
// at types.StageContext before a composed summary is persisted. A flow
// whose policy carries a compactor requires a configured guard at Build.
func WithCompactionGuard(g guards.Guard) Option {
	return func(c *config) error {
		c.contextGuard = g
		return nil
	}
}

// WithContextPolicy binds the context policy every registered native flow
// resolves with. A zero policy keeps a flow on the plain assembly path:
// no projection stage runs and no compactor is consulted.
func WithContextPolicy(p stores.ContextPolicy) Option {
	return func(c *config) error {
		for i := range c.native {
			c.native[i].ContextPolicy = p
		}
		return nil
	}
}

// WithLogger sets the structured logger for build-time records.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) error {
		if l == nil {
			return errors.New("gohan: nil logger")
		}
		c.logger = l
		return nil
	}
}

// WithTelemetry sets the port the stack emits spans and metrics through.
// The implementation lives outside core; a stack built without one emits
// nothing.
func WithTelemetry(t types.Telemetry) Option {
	return func(c *config) error {
		c.telemetry = t
		return nil
	}
}

// WithPinnedManifest pins the reviewed hashes of the registered tool set;
// drift against the current specs fails the build.
func WithPinnedManifest(m types.PinnedManifest) Option {
	return func(c *config) error {
		c.pinned = &m
		return nil
	}
}

// SequentialTools disables parallel tool execution for the stack.
func SequentialTools() Option {
	return func(c *config) error {
		c.sequentialTools = true
		return nil
	}
}

// AllowAnonymous permits principal-less invocation of unowned function
// flows. It invents no tenant, grants no access to a session that already
// has an owner, and bypasses no store authorization.
func AllowAnonymous() Option {
	return func(c *config) error {
		c.allowAnonymous = true
		return nil
	}
}

// AllowCache opts a flow's CacheStep out of the conversation and
// read-only refusals. The opt-out is never silent: Explain prints it as a
// warning on every flow that carries a cache step.
func AllowCache() Option {
	return func(c *config) error {
		c.allowCache = true
		return nil
	}
}

// MaxParallelTools bounds concurrent read-only tool calls. The value is
// carried by the option itself; the run-limits defaults do not supply it.
func MaxParallelTools(n int) Option {
	return func(c *config) error {
		if n < 1 {
			return fmt.Errorf("max parallel tools %d: %w", n, ErrNotPositive)
		}
		c.maxParallelTools = n
		return nil
	}
}

// WithTitleProfile names the profile the title recipe runs on. An unset
// option resolves to the cheapest configured profile at Build.
func WithTitleProfile(profile string) Option {
	return func(c *config) error {
		c.titleProfile = profile
		return nil
	}
}
