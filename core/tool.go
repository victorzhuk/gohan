package gohan

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// defaultMaxOutput is the tools spec's inline output cap: content beyond
// it moves to the output store.
const defaultMaxOutput = 64 << 10

// toolConfig collects everything the NewTool options touch: the spec the
// tool value carries and the schema derivation knobs the driver maps onto
// the types.SchemaBuilder.
type toolConfig struct {
	spec   types.ToolSpec
	schema []types.SchemaOption
}

// ToolOption adjusts a tool at construction. NewTool maps each option onto
// the spec and the schema builder.
type ToolOption func(*toolConfig)

// WithEffect sets the declared effect and defaults the risk from it:
// ReadOnly→Low, Idempotent→Medium, SideEffect→High.
func WithEffect(e types.Effect) ToolOption {
	return func(c *toolConfig) {
		c.spec.Effect = e
		c.spec.Risk = defaultRisk(e)
	}
}

// WithScopes lists the scopes a principal needs to call the tool.
func WithScopes(scopes ...string) ToolOption {
	return func(c *toolConfig) { c.spec.RequiredScopes = scopes }
}

// WithTimeout overrides the effect-derived default timeout.
func WithTimeout(d time.Duration) ToolOption {
	return func(c *toolConfig) { c.spec.Timeout = d }
}

// WithRisk overrides the effect-derived risk classification.
func WithRisk(r types.RiskTier) ToolOption {
	return func(c *toolConfig) { c.spec.Risk = r }
}

// Deferred registers the tool as governed but not prompt-loaded until
// discovered.
func Deferred() ToolOption {
	return func(c *toolConfig) { c.spec.Deferred = true }
}

// WithRawArgs allows untyped argument fields in the schema.
func WithRawArgs() ToolOption {
	return func(c *toolConfig) { c.schema = append(c.schema, types.WithRawArgs()) }
}

// ExcludeFields removes the named fields from the derived schema:
// identity is injected by the runtime, never set by the model.
func ExcludeFields(names ...string) ToolOption {
	return func(c *toolConfig) {
		c.schema = append(c.schema, types.WithIdentityFields(NewIdentityFieldMatcher(names...)))
	}
}

func defaultRisk(e types.Effect) types.RiskTier {
	switch e {
	case types.SideEffect:
		return types.RiskHigh
	case types.Idempotent:
		return types.RiskMedium
	default:
		return types.RiskLow
	}
}

func defaultTimeout(e types.Effect) time.Duration {
	switch e {
	case types.SideEffect:
		return 60 * time.Second
	case types.Idempotent:
		return 30 * time.Second
	default:
		return 10 * time.Second
	}
}

// tool is the concrete value NewTool returns: built once, shared by every
// run, with all per-request state taken from ctx and args.
type tool[In, Out any] struct {
	spec types.ToolSpec
	fn   func(context.Context, In) (Out, error)
}

// Spec returns the tool's declaration.
func (t *tool[In, Out]) Spec() types.ToolSpec { return t.spec }

// Call runs the tool function under the spec's timeout and maps the
// result onto model-visible blocks. The returned error is the tool
// function's own; classification into an Outcome happens on the tool step.
func (t *tool[In, Out]) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	var in In
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return types.ToolResult{}, fmt.Errorf("gohan: tool arguments: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, t.spec.Timeout)
	defer cancel()
	out, err := t.fn(ctx, in)
	if err != nil {
		return types.ToolResult{}, err
	}
	return outBlocks(out)
}

// NewTool builds a tool value from an input struct type and a function.
// The schema is derived from In once, here, in strict-compatible shape.
// The name grammar is enforced at construction: an invalid name returns
// types.ErrToolName and the tool never reaches Build.
//
// Build owns the cross-tool checks: CheckToolCollisions for name and
// reserved-name collisions, CheckToolSet for manifest renames.
func NewTool[In, Out any](name, desc string, fn func(context.Context, In) (Out, error), opts ...ToolOption) (*tool[In, Out], error) {
	if err := CheckToolName(name); err != nil {
		return nil, err
	}
	cfg := toolConfig{spec: types.ToolSpec{
		Name:        name,
		Description: desc,
		Effect:      types.ReadOnly,
		Risk:        types.RiskLow,
		MaxOutput:   defaultMaxOutput,
	}}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.spec.Timeout == 0 {
		cfg.spec.Timeout = defaultTimeout(cfg.spec.Effect)
	}
	schema, err := types.NewSchemaBuilder(cfg.schema...).Schema(reflect.TypeFor[In]())
	if err != nil {
		return nil, fmt.Errorf("gohan: tool %s schema: %w", name, err)
	}
	cfg.spec.Schema = schema
	return &tool[In, Out]{spec: cfg.spec, fn: fn}, nil
}
