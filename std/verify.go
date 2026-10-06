package std

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	gohan "github.com/victorzhuk/gohan/core"
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

// Reconcile settles one Unknown journal entry for tool. A successful
// reconciliation records the verified outcome and its audit record and
// reports settled; a persistence failure leaves the entry Unknown and
// reports the error, so the key stays in the uncertainty set.
func (v *Verifier) Reconcile(ctx context.Context, tool string, args json.RawMessage, key types.CallKey, res types.ToolResult) (types.ToolResult, bool, error) {
	if res.Outcome != types.Unknown || v.Specs == nil {
		return res, false, nil
	}
	spec, ok := v.Specs(tool)
	if !ok || spec.Verify == nil {
		return res, false, nil
	}
	audit, ok := gohan.AuditLogFrom(ctx)
	if !ok || audit == nil {
		return res, false, types.ErrVerifyAuditUnbound
	}
	verifyEntry, created, err := v.Journal.Reserve(ctx, key, tool+"/verify", CanonicalFingerprint(tool+"/verify", args), args)
	if err != nil {
		return res, false, fmt.Errorf("reserve verify: %w", err)
	}
	outcome := verifyEntry.Result.Outcome
	if created || verifyEntry.State != stores.Completed {
		outcome, err = spec.Verify(ctx, args, res)
		if err != nil {
			return res, false, fmt.Errorf("verify booking: %w", err)
		}
		verifyResult := types.ToolResult{ID: key.CallID, Outcome: outcome}
		if err := v.Journal.Complete(ctx, key, verifyResult); err != nil {
			return res, false, fmt.Errorf("complete verify: %w", err)
		}
	}
	verified := res
	verified.Outcome = outcome
	run, _ := gohan.RunInfoFrom(ctx)
	encoded, err := json.Marshal(types.Message{Blocks: verified.Content})
	if err != nil {
		return res, false, fmt.Errorf("encode verify result: %w", err)
	}
	sum := sha256.Sum256(encoded)
	if err := v.Journal.Complete(ctx, types.CallKey{SessionID: key.SessionID, CallID: res.ID}, verified); err != nil {
		return res, false, fmt.Errorf("complete verified result: %w", err)
	}
	record := stores.AuditRecord{
		Kind: stores.AuditToolOutcome, SessionID: key.SessionID, RunID: run.RunID,
		RootRunID: run.RootRunID, Flow: run.Flow, Subject: run.Principal.Subject, Tenant: run.Principal.Tenant,
		Tool: tool, Outcome: outcome, ResultSHA: hex.EncodeToString(sum[:]), ResultBytes: len(encoded),
	}
	if err := audit.Append(ctx, record); err != nil {
		return res, false, fmt.Errorf("append verify audit: %w", err)
	}
	return verified, true, nil
}
