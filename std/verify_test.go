package std

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func reserveUnknown(t *testing.T, j stores.Journal, session, callID, tool string) stores.Entry {
	t.Helper()
	key := types.CallKey{SessionID: session, CallID: callID}
	fp := stores.Fingerprint("fp-" + callID)
	if _, created, err := j.Reserve(context.Background(), key, tool, fp, nil); err != nil || !created {
		t.Fatalf("Reserve: created=%v err=%v", created, err)
	}
	if err := j.Complete(context.Background(), key, types.ToolResult{ID: callID, Outcome: types.Unknown}); err != nil {
		t.Fatal(err)
	}
	entries, err := j.ByFingerprint(context.Background(), session, fp)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ByFingerprint: %d entries, err=%v", len(entries), err)
	}
	return entries[0]
}

func toolFor(callID string) string {
	if callID == "c2" {
		return "send_fax"
	}
	return "create_booking"
}

func TestVerifyReconciliation(t *testing.T) {
	ctx := gohan.WithDecisionAudit(context.Background(), stores.NewMemoryAuditLog())

	t.Run("tools.verify-reconciles-unknown", func(t *testing.T) {
		j := stores.NewMemoryJournal()
		entry := reserveUnknown(t, j, "s1", "c1", toolFor("c1"))
		verifyCalls := 0
		specs := map[string]types.ToolSpec{
			"create_booking": {
				Name:   "create_booking",
				Effect: types.SideEffect,
				Verify: func(ctx context.Context, args json.RawMessage, r types.ToolResult) (types.Outcome, error) {
					verifyCalls++
					return types.Succeeded, nil
				},
			},
		}
		v := Verifier{Journal: j, Specs: lookupOf(specs)}
		verified, reconciled, err := v.Reconcile(ctx, "create_booking", json.RawMessage(`{"room":7}`), types.CallKey{SessionID: "s1", CallID: "c1/verify"}, entry.Result)
		if err != nil {
			t.Fatal(err)
		}
		if !reconciled {
			t.Fatal("entry was not reconciled")
		}
		if verified.Outcome != types.Succeeded {
			t.Fatalf("verified outcome = %v, want Succeeded", verified.Outcome)
		}
		if verifyCalls != 1 {
			t.Fatalf("verify calls = %d, want 1", verifyCalls)
		}
		entries, _ := j.ByFingerprint(ctx, "s1", stores.Fingerprint("fp-c1"))
		if len(entries) != 1 || entries[0].State != stores.Completed || entries[0].Result.Outcome != types.Succeeded {
			t.Fatalf("journal entry = %+v, want Completed Succeeded", entries)
		}
		if keys := UncertainKeys("s1", entries); len(keys) != 0 {
			t.Fatalf("uncertain keys = %v, want none", keys)
		}
	})

	t.Run("tools.verify-read-only", func(t *testing.T) {
		j := stores.NewMemoryJournal()
		entry := reserveUnknown(t, j, "s1", "c1", toolFor("c1"))
		toolCalls, verifyCalls := 0, 0
		tool := &countingTool{onCall: func() { toolCalls++ }}
		specs := map[string]types.ToolSpec{
			"create_booking": {
				Name:   "create_booking",
				Effect: types.SideEffect,
				Verify: func(ctx context.Context, args json.RawMessage, r types.ToolResult) (types.Outcome, error) {
					verifyCalls++
					_ = tool
					return types.Succeeded, nil
				},
			},
		}
		v := Verifier{Journal: j, Specs: lookupOf(specs)}
		if _, _, err := v.Reconcile(ctx, "create_booking", json.RawMessage(`{"room":7}`), types.CallKey{SessionID: "s1", CallID: "c1/verify"}, entry.Result); err != nil {
			t.Fatal(err)
		}
		if toolCalls != 0 {
			t.Fatalf("tool calls = %d, want 0", toolCalls)
		}
		if verifyCalls != 1 {
			t.Fatalf("verify calls = %d, want 1", verifyCalls)
		}
		verifyEntries, _ := j.ByFingerprint(ctx, "s1", CanonicalFingerprint("create_booking/verify", json.RawMessage(`{"room":7}`)))
		if len(verifyEntries) != 1 || verifyEntries[0].Key != "c1/verify" {
			t.Fatalf("/verify journal = %+v, want one entry keyed c1/verify", verifyEntries)
		}
	})

	t.Run("tools.verify-error", func(t *testing.T) {
		j := stores.NewMemoryJournal()
		entry := reserveUnknown(t, j, "s1", "c1", toolFor("c1"))
		specs := map[string]types.ToolSpec{
			"create_booking": {
				Name: "create_booking",
				Verify: func(ctx context.Context, args json.RawMessage, r types.ToolResult) (types.Outcome, error) {
					return types.Unknown, errors.New("verify unavailable")
				},
			},
		}
		v := Verifier{Journal: j, Specs: lookupOf(specs)}
		res, reconciled, err := v.Reconcile(ctx, "create_booking", json.RawMessage(`{}`), types.CallKey{SessionID: "s1", CallID: "c1/verify"}, entry.Result)
		if err == nil {
			t.Fatal("want the verify error to surface")
		}
		if reconciled {
			t.Fatal("entry must not reconcile on a verify error")
		}
		if res.Outcome != types.Unknown {
			t.Fatalf("outcome = %v, want Unknown", res.Outcome)
		}
		entries, _ := j.ByFingerprint(ctx, "s1", stores.Fingerprint("fp-c1"))
		if entries[0].Result.Outcome != types.Unknown {
			t.Fatalf("journal outcome = %v, want Unknown", entries[0].Result.Outcome)
		}
	})

	t.Run("tools.verify-on-recover", func(t *testing.T) {
		j := stores.NewMemoryJournal()
		verifiedEntry := reserveUnknown(t, j, "s1", "c1", toolFor("c1"))
		plainEntry := reserveUnknown(t, j, "s1", "c2", toolFor("c2"))
		specs := map[string]types.ToolSpec{
			"create_booking": {
				Name:   "create_booking",
				Effect: types.SideEffect,
				Verify: func(ctx context.Context, args json.RawMessage, r types.ToolResult) (types.Outcome, error) {
					return types.Succeeded, nil
				},
			},
			"send_fax": {Name: "send_fax", Effect: types.SideEffect},
		}
		v := Verifier{Journal: j, Specs: lookupOf(specs)}
		if _, reconciled, err := v.Reconcile(ctx, "create_booking", json.RawMessage(`{}`), types.CallKey{SessionID: "s1", CallID: "c1/verify"}, verifiedEntry.Result); err != nil || !reconciled {
			t.Fatalf("reconciled=%v err=%v, want true nil", reconciled, err)
		}
		res, reconciled, err := v.Reconcile(ctx, "send_fax", json.RawMessage(`{}`), types.CallKey{SessionID: "s1", CallID: "c2/verify"}, plainEntry.Result)
		if err != nil {
			t.Fatal(err)
		}
		if reconciled {
			t.Fatal("entry without Verify must stay uncertain")
		}
		if res.Outcome != types.Unknown {
			t.Fatalf("outcome = %v, want Unknown", res.Outcome)
		}
		e1, _ := j.ByFingerprint(ctx, "s1", stores.Fingerprint("fp-c1"))
		e2, _ := j.ByFingerprint(ctx, "s1", stores.Fingerprint("fp-c2"))
		keys := UncertainKeys("s1", append(e1, e2...))
		if len(keys) != 1 || keys[0].CallID != "c2" {
			t.Fatalf("uncertain = %v, want only c2", keys)
		}
	})

	t.Run("unbound audit is an error", func(t *testing.T) {
		j := stores.NewMemoryJournal()
		entry := reserveUnknown(t, j, "s1", "c1", toolFor("c1"))
		specs := map[string]types.ToolSpec{
			"create_booking": {
				Name:   "create_booking",
				Effect: types.SideEffect,
				Verify: func(ctx context.Context, args json.RawMessage, r types.ToolResult) (types.Outcome, error) {
					return types.Succeeded, nil
				},
			},
		}
		v := Verifier{Journal: j, Specs: lookupOf(specs)}
		res, reconciled, err := v.Reconcile(context.Background(), "create_booking", json.RawMessage(`{}`), types.CallKey{SessionID: "s1", CallID: "c1/verify"}, entry.Result)
		if !errors.Is(err, types.ErrVerifyAuditUnbound) {
			t.Fatalf("err = %v, want types.ErrVerifyAuditUnbound", err)
		}
		if reconciled {
			t.Fatal("entry must not reconcile without an audit log")
		}
		if res.Outcome != types.Unknown {
			t.Fatalf("outcome = %v, want Unknown", res.Outcome)
		}
		entries, _ := j.ByFingerprint(context.Background(), "s1", stores.Fingerprint("fp-c1"))
		if len(entries) != 1 || entries[0].Result.Outcome != types.Unknown {
			t.Fatalf("journal entry = %+v, want Unknown left in place", entries)
		}
	})
}

type countingTool struct {
	onCall func()
}

func (c *countingTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "create_booking", Effect: types.SideEffect}
}

func (c *countingTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	c.onCall()
	return types.ToolResult{Outcome: types.Succeeded}, nil
}
