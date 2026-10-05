package gohan

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Option adjusts Build. Every option takes a floor type or interface: the
// driver never imports std, so the caller passes the std policy value.
type Option func(*config) error

type config struct {
	models           []types.Model
	middleware       []types.ModelMiddleware
	estimator        types.TokenEstimator
	keys             types.ProviderKeySource
	credentials      types.CredentialSource
	approvalPolicy   permission.ApprovalPolicySource
	logger           *slog.Logger
	telemetry        types.Telemetry
	pinned           *types.PinnedManifest
	prompts          chains.PromptSet
	sequentialTools  bool
	allowAnonymous   bool
	maxParallelTools int
	limits           map[string]types.RunLimits
	recovery         map[string]runtime.Runtime
	stores           stores.Stores
	native           []NativeSpec
}

// WithStores binds the store ports Recover runs against. A zero field
// means the operation that needs it is refused.
func WithStores(s stores.Stores) Option {
	return func(c *config) error {
		c.stores = s
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

// WithApprovalPolicySource sets the source Conversation approval decisions
// resolve their policy from. The caller passes the floor interface value;
// std supplies the default implementation.
func WithApprovalPolicySource(src permission.ApprovalPolicySource) Option {
	return func(c *config) error {
		c.approvalPolicy = src
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
