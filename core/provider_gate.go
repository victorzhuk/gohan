package gohan

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

// providerDeclarationGate runs the pre-call gate on every registered
// provider-executed spec with empty arguments, before the request is
// assembled and sent: the call happens inside the model request, so the
// spec is the only pre-call input. Ordering inside the gate is the sealed
// one — scopes, live deny, taint hook, decider — reused, not re-implemented.
// The verdict maps onto the declaration: Deny omits the tool, Ask declares
// it with the provider approval flag only when the profile's caps mark the
// tool approval capable and otherwise omits it, Allow declares it.
func providerDeclarationGate(cfg *resolvedNativeConfig) func(context.Context, types.ModelRequest) (types.ModelRequest, error) {
	supported := cfg.profile.Caps.ProviderTools
	return func(ctx context.Context, req types.ModelRequest) (types.ModelRequest, error) {
		run, _ := types.RunInfoFrom(ctx)
		opts := []permission.Option{
			permission.WithRunInfo(func(context.Context) types.RunInfo { return run }),
			permission.WithTaintHook(cfg.taintPolicy),
			permission.WithLiveControl(cfg.liveControl),
			permission.WithControlWait(cfg.controlWait),
		}
		var declared []types.ToolSpec
		for _, spec := range req.Tools {
			if spec.Executor != types.ByProvider {
				declared = append(declared, spec)
				continue
			}
			// The provider executes the call inside Generate, so a
			// shadow run can never suppress it after the fact: the
			// declaration is omitted unless the tool is read-only.
			if run.Mode == types.Shadow {
				if spec.Effect != types.ReadOnly {
					continue
				}
			}
			dec := permission.DecideProviderTool(ctx, cfg.decider, spec, opts...)
			switch dec.Verdict {
			case permission.ProviderDeclare:
				declared = append(declared, spec)
			case permission.ProviderDeclareApproval:
				if supported[spec.Name].Approval {
					declared = append(declared, spec)
				}
			}
		}
		req.Tools = declared
		return req, nil
	}
}

// providerSpec reports whether the named tool is registered as a
// provider-executed spec.
func providerSpec(specs []types.ToolSpec, name string) bool {
	for _, s := range specs {
		if s.Name == name {
			return s.Executor == types.ByProvider
		}
	}
	return false
}

// providerApprovalPending records the provider call the provider asked
// approval for as the run's single pending call and returns the suspension
// the turn effect returns: HumanApproval, with the call under its original
// identity so the resumed replay and the journal keep the same CallKey. The
// taints the pre-call hook produced travel with the batch continuation, so
// the suspension payload's ArgOrigins carry them.
func providerApprovalPending(st *runtime.State, call types.ToolUse, taints []types.ArgTaint) (*types.SuspendError, error) {
	b, err := decodeNativeBackend(*st)
	if err != nil {
		return nil, err
	}
	if b.Driver == nil {
		b.Driver = &nativeProgress{Version: nativeProgressVersion}
	}
	b.Driver.Batch = &nativeBatchContinuation{Calls: []types.ToolUse{call}, Taints: taints}
	enc, err := encodeNativeBackend(b)
	if err != nil {
		return nil, err
	}
	st.Backend = enc
	st.Pending = []types.ToolUse{call}
	return &types.SuspendError{Reason: types.HumanApproval}, nil
}

// replayProviderApproval settles an approved provider ask on resume: the
// call never executes in the harness, so the ask clears and the turn loop
// re-enters the model effect, replaying the model call whose approval item
// carries the call's original identity.
func replayProviderApproval(st *runtime.State, b nativeBackend, slot int) (json.RawMessage, error) {
	calls := make([]types.ToolUse, 0, len(b.Driver.Batch.Calls))
	for i, call := range b.Driver.Batch.Calls {
		if i == slot {
			continue
		}
		calls = append(calls, call)
	}
	b.Driver.Batch.Calls = calls
	enc, err := encodeNativeBackend(b)
	if err != nil {
		return nil, fmt.Errorf("%w: encode provider replay: %s", types.ErrCheckpointIncompatible, err)
	}
	st.Pending = nil
	return enc, nil
}
