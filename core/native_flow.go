package gohan

import (
	"fmt"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

// RecipeCall is one governed recipe model call: the middleware composition
// the native conversation runs, wrapped around the selected profile's
// model. Provider names the profile, so a recipe reports model errors
// without holding the model itself.
type RecipeCall struct {
	Call     types.ModelFunc
	Provider string
}

// RecipeModel resolves one profile's model and wraps it in the same
// middleware composition the native path uses. An unknown profile refuses
// at construction, before any provider call.
func (s *Stack) RecipeModel(profile string) (RecipeCall, error) {
	m, ok := modelForProfile(s.models, profile)
	if !ok {
		return RecipeCall{}, fmt.Errorf("gohan: recipe profile %q not configured", profile)
	}
	return RecipeCall{
		Call:     runModelChain(modelChainWithMiddleware(nil, s.middleware), m.Generate),
		Provider: m.Profile().Name,
	}, nil
}

// RequirePrompts refuses a declared prompt consumer when one of its
// required PromptSet fields is empty. The refusal names the consumer and
// the field, so a caller surfaces the gap at construction rather than
// sending a half-configured prompt to a model.
func RequirePrompts(ps chains.PromptSet, consumer string, fields ...string) error {
	for _, f := range fields {
		if promptField(ps, f) == "" {
			return fmt.Errorf("gohan: prompt consumer %q: prompt field %q is empty", consumer, f)
		}
	}
	return nil
}

func promptField(ps chains.PromptSet, name string) string {
	switch name {
	case "FenceOpen":
		return ps.FenceOpen
	case "FenceClose":
		return ps.FenceClose
	case "DataNotInstructions":
		return ps.DataNotInstructions
	case "OutcomeUnknown":
		return ps.OutcomeUnknown
	case "ReadBackHint":
		return ps.ReadBackHint
	case "OutputRefHint":
		return ps.OutputRefHint
	case "RepairInstruction":
		return ps.RepairInstruction
	case "NotesPreamble":
		return ps.NotesPreamble
	case "OperatorTurn":
		return ps.OperatorTurn
	case "Version":
		return ps.Version
	}
	return ""
}
