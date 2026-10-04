package stores

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestAuditLog(t *testing.T) {
	t.Run("stores.chain-written-only", func(t *testing.T) {
		base := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
		tick := base
		log := NewMemoryAuditLog(WithMemoryAuditClock(func() time.Time {
			tick = tick.Add(time.Second)
			return tick
		}))

		// No exported path lets a caller set the chain fields: Append
		// overwrites whatever PrevHash and Hash a caller supplied.
		first := AuditRecord{Kind: AuditGuard, SessionID: "s1", Tenant: "t1"}
		if err := log.Append(context.Background(), first); err != nil {
			t.Fatalf("Append(first) = %v", err)
		}
		forged := AuditRecord{Kind: AuditGuard, SessionID: "s1", Tenant: "t1", PrevHash: "deadbeef", Hash: "forged"}
		if err := log.Append(context.Background(), forged); err != nil {
			t.Fatalf("Append(forged) = %v", err)
		}

		var got []AuditRecord
		for rec, err := range log.Read(context.Background(), "s1") {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			got = append(got, rec)
		}
		if len(got) != 2 {
			t.Fatalf("len(records) = %d, want 2", len(got))
		}
		if got[0].PrevHash != "" || got[0].Hash == "" {
			t.Fatalf("first record chain = {%q, %q}, want genesis and a seal", got[0].PrevHash, got[0].Hash)
		}
		if got[1].PrevHash != got[0].Hash {
			t.Fatalf("second PrevHash = %q, want %q", got[1].PrevHash, got[0].Hash)
		}
		if got[1].Hash == "forged" || got[1].PrevHash == "deadbeef" {
			t.Fatal("caller-supplied chain fields survived Append")
		}
		want := auditHash(func() AuditRecord {
			r := got[1]
			r.PrevHash, r.Hash = got[0].Hash, ""
			return r
		}())
		if got[1].Hash != want {
			t.Fatalf("Hash = %q, want recomputed %q", got[1].Hash, want)
		}
	})

	t.Run("stores.no-content", func(t *testing.T) {
		log := NewMemoryAuditLog()
		rec := AuditRecord{
			Kind:        AuditToolOutcome,
			SessionID:   "s2",
			Tenant:      "t1",
			Tool:        "crm.lookup",
			ResultSHA:   "9f2b0c",
			ResultBytes: 51200,
		}
		if err := log.Append(context.Background(), rec); err != nil {
			t.Fatalf("Append = %v", err)
		}
		for got, err := range log.Read(context.Background(), "s2") {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			// AuditRecord has no field carrying result or arg content:
			// only the checksum and size survive, so the 50 KiB payload
			// with its email address cannot be in the record.
			if got.ResultSHA != "9f2b0c" || got.ResultBytes != 51200 {
				t.Fatalf("result reference = {%q, %d}, want {9f2b0c, 51200}", got.ResultSHA, got.ResultBytes)
			}
			return
		}
		t.Fatal("record not returned")
	})

	t.Run("stores.append-failure-is-fatal-to-the-step", func(t *testing.T) {
		log := NewMemoryAuditLog()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := log.Append(ctx, AuditRecord{Kind: AuditGuard, SessionID: "s3", Tenant: "t1"})
		if err == nil {
			t.Fatal("Append with a canceled context = nil, want the context error")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Append error = %v, want context.Canceled", err)
		}
		// The step-failure contract: the harness wraps the append error in
		// a StepError naming the step, and the run fails through errors.As.
		stepErr := &types.StepError{Step: "gate", Err: err}
		var got *types.StepError
		if !errors.As(fmt.Errorf("gate step: %w", stepErr), &got) || got.Step != "gate" {
			t.Fatalf("errors.As = %v, want *StepError{Step: gate}", got)
		}
		if !errors.Is(stepErr, context.Canceled) {
			t.Fatal("wrapped append error is not reachable through StepError")
		}
	})
}
