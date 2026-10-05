// Package flow ships the governed single-call recipes Extract and Classify
// (flow.extract-recipe, flow.classify-recipe). Dependency direction: this
// leaf imports the floor core/types and the driver package gohan for the
// Flow contract; it imports no other core package.
package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/structured"
)

// Option configures a recipe's bounded repair behavior.
type Option func(*config)

type config struct {
	repairs int
}

func (c config) attempts() int { return 1 + c.repairs }

// WithRepairs bounds the repair turns a recipe spends before it refuses
// (structured-output.validate-and-repair). The default is one.
func WithRepairs(n int) Option {
	return func(c *config) {
		if n >= 0 {
			c.repairs = n
		}
	}
}

// Extract returns a Flow over document blocks that makes one model call
// with the strict schema derived for Out, validates the decoded value
// through the structured-output validator, and returns the typed value
// only after validation passes. The governed model call and the repair
// instruction come from the Stack and the named profile; construction
// problems surface at Invoke, before any provider call. No tool is ever
// offered.
func Extract[Out any](s *gohan.Stack, profile string, opts ...Option) gohan.Flow[[]types.Block, Out] {
	c := newConfig(opts)
	schema, schemaErr := schemaFor[Out]()
	rc, repair, buildErr := bind(s, profile, "flow.extract")
	if buildErr == nil {
		buildErr = schemaErr
	}
	return gohan.FlowFunc[[]types.Block, Out]("flow.extract", func(ctx context.Context, in []types.Block) (Out, error) {
		if buildErr != nil {
			var zero Out
			return zero, buildErr
		}
		var out Out
		err := call(ctx, rc, repair, schema, in, c, func(text string) (any, error) {
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				return nil, err
			}
			if err := structured.Validate(out); err != nil {
				return nil, err
			}
			return out, nil
		})
		return out, err
	})
}

// Classify returns a Flow that makes one model call constrained to a
// single label from labels. A value outside the label set takes one
// ValidateRepair turn, then fails as a Permanent model error
// (flow.classify-recipe). The governed model call and the repair
// instruction come from the Stack and the named profile; construction
// problems surface at Invoke, before any provider call. No tool is ever
// offered.
func Classify[L ~string](s *gohan.Stack, profile string, labels []L, opts ...Option) gohan.Flow[[]types.Block, L] {
	c := newConfig(opts)
	schema, schemaErr := labelSchema(labels)
	rc, repair, buildErr := bind(s, profile, "flow.classify")
	return gohan.FlowFunc[[]types.Block, L]("flow.classify", func(ctx context.Context, in []types.Block) (L, error) {
		if buildErr != nil {
			var zero L
			return zero, buildErr
		}
		if schemaErr != nil {
			var zero L
			return zero, schemaErr
		}
		var out L
		err := call(ctx, rc, repair, schema, in, c, func(text string) (any, error) {
			var choice struct {
				Label L `json:"label"`
			}
			if err := json.Unmarshal([]byte(text), &choice); err != nil {
				return nil, err
			}
			for _, l := range labels {
				if choice.Label == l {
					out = choice.Label
					return out, nil
				}
			}
			return nil, outOfSetError{fmt.Errorf("label %q outside the label set", choice.Label)}
		})
		return out, err
	})
}

// bind resolves the recipe's governed model call and the caller's repair
// instruction from the Stack. A nil stack or an unknown profile becomes
// the constructor error the flow returns before its first provider call.
func bind(s *gohan.Stack, profile, consumer string) (gohan.RecipeCall, string, error) {
	if s == nil {
		return gohan.RecipeCall{}, "", fmt.Errorf("%s: nil stack", consumer)
	}
	rc, err := s.RecipeModel(profile)
	if err != nil {
		return gohan.RecipeCall{}, "", fmt.Errorf("%s: %w", consumer, err)
	}
	return rc, s.Prompts().RepairInstruction, nil
}

func newConfig(opts []Option) config {
	c := config{repairs: 1}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// outOfSetError marks a decode that produced a well-formed value outside
// the recipe's accepted set: after the repair turns are spent, it is a
// Permanent model error, not a schema failure.
type outOfSetError struct{ err error }

func (e outOfSetError) Error() string { return e.err.Error() }

// call drains one governed model call per attempt and classifies the
// result: a decode or validation failure spends a repair turn, a refusal
// phrased as schema-valid JSON is returned as its classified model error,
// and a passing attempt returns the text for the caller to decode. The
// repair turn speaks only the caller's RepairInstruction; a set without
// one adds no prompt text.
func call(ctx context.Context, rc gohan.RecipeCall, repair string, schema json.RawMessage, in []types.Block, c config, decode func(string) (any, error)) error {
	req := types.ModelRequest{
		Messages: []types.Message{{Role: types.RoleUser, Blocks: in}},
		Options:  types.ModelOptions{ResponseSchema: schema},
	}
	var lastErr error
	for range c.attempts() {
		text, err := drain(ctx, rc.Call, req)
		if err != nil {
			return err
		}
		value, err := decode(text)
		switch {
		case err == nil:
			if class, refused := structured.Classify(value); refused {
				return &types.ModelError{Class: class, Provider: rc.Provider}
			}
			return nil
		case errors.As(err, new(outOfSetError)):
			lastErr = &types.ModelError{Class: types.ClassPermanent, Provider: rc.Provider}
		default:
			lastErr = fmt.Errorf("structured output: %w: %s", types.ErrStructuredOutput, err)
		}
		req.Messages = append(req.Messages,
			types.Message{Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: text}}},
		)
		if repair != "" {
			req.Messages = append(req.Messages,
				types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: repair}}},
			)
		}
	}
	return lastErr
}

// drain collects the text deltas of one governed model call.
func drain(ctx context.Context, next types.ModelFunc, req types.ModelRequest) (string, error) {
	var text []byte
	for chunk, err := range next(ctx, req) {
		if err != nil {
			return "", err
		}
		if chunk.Kind == types.DeltaText {
			text = append(text, chunk.Delta...)
		}
	}
	return string(text), nil
}

// schemaFor derives the strict schema through the floor's SchemaBuilder,
// never a second derivation.
func schemaFor[Out any]() (json.RawMessage, error) {
	return structured.ToolSchema{Strict: true}.Schema(reflect.TypeFor[Out]())
}

// labelSchema derives the strict schema for the single-label choice and
// pins the caller's label set onto it as the enum; the enum values are
// runtime data, so the tag walker cannot carry them.
func labelSchema[L ~string](labels []L) (json.RawMessage, error) {
	raw, err := structured.ToolSchema{Strict: true}.Schema(reflect.TypeFor[struct {
		Label L `json:"label" desc:"one label from the configured set"`
	}]())
	if err != nil {
		return nil, err
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("structured output: %w: %s", types.ErrStructuredOutput, err)
	}
	props, _ := schema["properties"].(map[string]any)
	label, _ := props["label"].(map[string]any)
	if label == nil {
		return nil, fmt.Errorf("structured output: %w: schema has no label property", types.ErrStructuredOutput)
	}
	enum := make([]any, len(labels))
	for i, l := range labels {
		enum[i] = string(l)
	}
	label["enum"] = enum
	out, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("structured output: %w: %s", types.ErrStructuredOutput, err)
	}
	return json.RawMessage(out), nil
}
