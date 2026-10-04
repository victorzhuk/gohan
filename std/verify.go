package std

import (
	"context"
	"encoding/json"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Verifier reconciles unknown outcomes through a tool's read-only Verify
// declaration. It never calls the tool itself: Verify runs instead of the
// effect, and the journal entry it produces sits under the verify key the
// caller mints.
type Verifier struct {
	Journal stores.Journal
	Specs   SpecLookup
}

// ReconcileUnknown verifies one Unknown journal entry for tool. When Verify
// reports Succeeded, the entry is completed with the verified result and
// reconciled is true; otherwise the entry stays Unknown and reconciled is
// false. A Verify error leaves the entry untouched and is returned.
func (v *Verifier) ReconcileUnknown(ctx context.Context, sessionID, tool string, args json.RawMessage, verifyKey types.CallKey, entry stores.Entry) (types.ToolResult, bool, error) {
	if entry.Result.Outcome != types.Unknown {
		return entry.Result, false, nil
	}
	if v.Specs == nil {
		return entry.Result, false, nil
	}
	spec, ok := v.Specs(tool)
	if !ok || spec.Verify == nil {
		return entry.Result, false, nil
	}
	outcome, err := spec.Verify(ctx, args, entry.Result)
	if err != nil {
		return entry.Result, false, err
	}
	_, _, _ = v.Journal.Reserve(ctx, verifyKey, CanonicalFingerprint(tool+"/verify", args))
	_ = v.Journal.Complete(ctx, verifyKey, types.ToolResult{ID: verifyKey.CallID, Outcome: outcome})
	if outcome != types.Succeeded {
		return entry.Result, false, nil
	}
	verified := entry.Result
	verified.Outcome = types.Succeeded
	verified.Error = nil
	if err := v.Journal.Complete(ctx, types.CallKey{SessionID: sessionID, CallID: entry.Key}, verified); err != nil {
		return verified, false, err
	}
	return verified, true, nil
}
