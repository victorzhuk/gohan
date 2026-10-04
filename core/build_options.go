package gohan

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/victorzhuk/gohan/core/chains"
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
	logger           *slog.Logger
	pinned           *types.PinnedManifest
	prompts          chains.PromptSet
	sequentialTools  bool
	maxParallelTools int
	limits           map[string]types.RunLimits
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
