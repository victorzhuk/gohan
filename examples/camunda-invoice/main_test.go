package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func resetExecuted() { executed = nil }

func testJob() invoiceJob {
	return invoiceJob{
		OperationID: "INV-2026-0042",
		Invoice:     "INV-2026-0042",
		AmountCents: 149500,
	}
}

func TestCamundaInvoiceOffline(t *testing.T) {
	ctx := context.Background()

	t.Run("engines.duplicate-workers", func(t *testing.T) {
		resetExecuted()
		clock := &fakeClock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
		f := newFlow(clock)
		job := testJob()
		ready := make(chan struct{})
		errs := make(chan struct {
			lease stores.Lease
			run   stores.Run
			err   error
		}, 2)
		var wg sync.WaitGroup
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-ready
				lease, run, err := duplicateWorker(ctx, f, job, fmt.Sprint(i))
				errs <- struct {
					lease stores.Lease
					run   stores.Run
					err   error
				}{lease, run, err}
			}()
		}
		close(ready)
		wg.Wait()
		close(errs)

		var wins, losses int
		var winLease stores.Lease
		var winRun stores.Run
		for res := range errs {
			if res.err == nil {
				wins++
				winLease, winRun = res.lease, res.run
				continue
			}
			var exists stores.OperationExistsError
			if !errors.As(res.err, &exists) {
				t.Fatalf("loser error %v, want OperationExistsError", res.err)
			}
			losses++
		}
		if wins != 1 || losses != 1 {
			t.Fatalf("got %d wins, %d losses; want exactly one of each", wins, losses)
		}
		out, err := f.Claimed(ctx, job, winLease, winRun)
		if err != nil {
			t.Fatalf("winner process: %v", err)
		}
		if !out.executed {
			t.Fatalf("winner outcome %+v, want the invoice sent once", out)
		}
		if got := len(executed); got != 1 {
			t.Fatalf("executed %d tools, want exactly 1", got)
		}
	})

	t.Run("engines.suspended-runs-are-not-reclaimed", func(t *testing.T) {
		resetExecuted()
		clock := &fakeClock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
		f := newFlow(clock)
		job := testJob()
		lease, run, err := startRun(ctx, f.runs, job)
		if err != nil {
			t.Fatalf("start run: %v", err)
		}
		if err := askApproval(ctx, f.runs, lease, approvalToken); err != nil {
			t.Fatalf("user task suspend: %v", err)
		}
		clock.Advance(3 * 24 * time.Hour)

		stale, err := f.runs.Stale(ctx, 72*time.Hour, 10)
		if err != nil {
			t.Fatalf("stale: %v", err)
		}
		if len(stale) != 0 {
			t.Fatalf("stale listed %v, want the suspended run left alone", stale)
		}
		if _, err := f.runs.Reclaim(ctx, run, stores.LeaseTTL); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("reclaim error %v, want ErrRunNotActive", err)
		}
		got, err := f.runs.ByOperation(ctx, "", job.OperationID)
		if err != nil {
			t.Fatalf("by operation: %v", err)
		}
		if got.State != stores.Suspended {
			t.Fatalf("run state %v, want Suspended", got.State)
		}
	})

	t.Run("engines.revoked-authority-on-resume", func(t *testing.T) {
		resetExecuted()
		clock := &fakeClock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
		f := newFlow(clock)
		f.resumeScopes = []string{approvalScope}
		out, err := f.Process(ctx, testJob())
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		if !out.denied || out.executed {
			t.Fatalf("outcome %+v, want the pending call denied on resume", out)
		}
		if out.reason != "originator scope revoked while suspended" {
			t.Fatalf("reason %q, want the revocation verdict", out.reason)
		}
		if len(executed) != 0 {
			t.Fatalf("executed %v, want nothing run", executed)
		}
	})

	t.Run("engines.stale-control-state", func(t *testing.T) {
		resetExecuted()
		clock := &fakeClock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
		f := newFlow(clock)
		f.outageOnResume = true
		out, err := f.Process(ctx, testJob())
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		if !out.suspended || out.denied {
			t.Fatalf("outcome %+v, want AwaitingControl while the outage is fresh", out)
		}

		clock.Advance(MaxControlWait + time.Minute)
		inv := invoiceInvocation(testJob())
		inv.Run = runInfo("run-"+testJob().OperationID, f.origin)
		kind, reason := resume(ctx, f.control, inv, clock.t.Add(-MaxControlWait-time.Minute), clock.t)
		if kind != resumeDenyControlWait {
			t.Fatalf("resume kind %v, want the control-wait denial", kind)
		}
		if reason == "" {
			t.Fatal("denial carries no reason")
		}
		if len(executed) != 0 {
			t.Fatalf("executed %v, want nothing run", executed)
		}
	})

	t.Run("live deny beats approval", func(t *testing.T) {
		resetExecuted()
		clock := &fakeClock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
		f := newFlow(clock)
		f.killOnResume = true
		out, err := f.Process(ctx, testJob())
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		if !out.denied || out.executed {
			t.Fatalf("outcome %+v, want the approved call denied by the live flag", out)
		}
		want := []string{"approved", "flag_denied"}
		if !slices.Equal(f.audit, want) {
			t.Fatalf("audit %v, want %v", f.audit, want)
		}
		if len(executed) != 0 {
			t.Fatalf("executed %v, want the tool never run", executed)
		}
	})
}
