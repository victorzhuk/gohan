package gohan

import (
	"context"
	"fmt"
	"reflect"

	"encoding/json/jsontext"

	"github.com/victorzhuk/gohan/core/types"
)

// ProviderCallBlock converts the provider's reported call block into the
// ToolUse the harness stores and journals: the executor is the registered
// spec's Executor, never a block field.
func ProviderCallBlock(spec types.ToolSpec, id string, args jsontext.Value) types.ToolUse {
	return types.ToolUse{
		BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}},
		ID:        id,
		Name:      spec.Name,
		Args:      args,
	}
}

// ProviderResultBlock converts the provider's reported result block into a
// ToolResult whose content carries the registered spec's provider origin.
// Opaque payloads the provider requires back stay inside the result as Raw
// and round-trip per the model capability's pass-through rule.
func ProviderResultBlock(spec types.ToolSpec, call types.ToolUse, content ...types.Block) types.ToolResult {
	origin := types.Origin{Kind: types.OriginProvider, Name: spec.Name}
	stamped := make([]types.Block, 0, len(content))
	for _, b := range content {
		if raw, ok := b.(types.Raw); ok {
			raw.Origin = origin
			stamped = append(stamped, raw)
			continue
		}
		stamped = append(stamped, blockWithOrigin(b, origin))
	}
	return types.ToolResult{
		BlockBase: types.BlockBase{Origin: origin},
		ID:        call.ID,
		Outcome:   types.Succeeded,
		Content:   stamped,
	}
}

func blockWithOrigin(b types.Block, origin types.Origin) types.Block {
	v := reflect.New(reflect.TypeOf(b)).Elem()
	v.Set(reflect.ValueOf(b))
	if base := v.FieldByName("BlockBase"); base.IsValid() && base.CanSet() {
		base.FieldByName("Origin").Set(reflect.ValueOf(origin))
	}
	return v.Interface().(types.Block)
}

// CountProviderCalls folds the provider-executed calls a response carried
// into the usage record the run charges: only calls whose registered spec
// is ByProvider count, one per reported block.
func CountProviderCalls(specs []types.ToolSpec, calls []types.ToolUse, u *types.Usage) {
	if u == nil {
		return
	}
	for _, call := range calls {
		if _, ok := providerSpecOf(specs, call.Name); !ok {
			continue
		}
		if u.ProviderToolCalls == nil {
			u.ProviderToolCalls = make(map[string]int)
		}
		u.ProviderToolCalls[call.Name]++
	}
}

// providerSpecOf returns the registered spec for a provider-executed tool.
func providerSpecOf(specs []types.ToolSpec, name string) (types.ToolSpec, bool) {
	for _, s := range specs {
		if s.Name == name && s.Executor == types.ByProvider {
			return s, true
		}
	}
	return types.ToolSpec{}, false
}

// providerTaintAbort aborts the run over a tainted input into an Exfil
// provider tool: the call is already spent, so the failure is permanent
// and no result the provider returned is used.
type providerTaintAbort struct {
	tool   string
	taints []types.ArgTaint
}

func (e providerTaintAbort) Error() string {
	return fmt.Sprintf("gohan: provider tool %q received tainted input: %s", e.tool, taintSummary(e.taints))
}

func taintSummary(taints []types.ArgTaint) string {
	s := ""
	for i, t := range taints {
		if i > 0 {
			s += ", "
		}
		s += t.Arg
	}
	return s
}

// providerPostHoc runs the taint hook over the arguments the provider
// reported once the response arrives, before any result block is appended.
func providerPostHoc(ctx context.Context, hook types.TaintHook, specs []types.ToolSpec, call types.ToolUse) error {
	if hook == nil {
		return nil
	}
	spec, ok := providerSpecOf(specs, call.Name)
	if !ok || !spec.Capabilities.Exfil {
		return nil
	}
	_, taints := hook(ctx, spec, call.Args)
	if len(taints) == 0 {
		return nil
	}
	if tel := telemetryFrom(ctx); tel != nil {
		tel.Count(context.Background(), "gohan.taint.post_hoc", 1, types.String("tool", spec.Name))
	}
	return &types.ModelError{
		Class: types.ClassPermanent,
		Err:   providerTaintAbort{tool: spec.Name, taints: taints},
	}
}
