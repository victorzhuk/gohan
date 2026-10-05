// Package main composes an offline invoice job: an external work item is
// fetched, a user task suspends the run for human approval, and the resume
// path re-checks live control state before any side effect runs. Memory
// stores and a fixed clock keep it offline: no network, no keys.
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const (
	// FreshnessLimit bounds how long the job trusts the last control
	// snapshot once the flags provider stops answering.
	FreshnessLimit = time.Hour
	// MaxControlWait bounds how long a run may sit suspended on
	// unresolved control state before the pending effect is denied.
	MaxControlWait = 3 * 24 * time.Hour
	// approvalScope authorises the human approval of the invoice job.
	approvalScope = "invoice.approve"
	// sendScope authorises executing the invoice send tool.
	sendScope = "invoice.send"
)

// controlPlane stands in for the live flags provider.
type controlPlane struct {
	mu        sync.Mutex
	reachable bool
	kill      bool
	seen      time.Time
}

func newControlPlane(now time.Time) *controlPlane {
	return &controlPlane{reachable: true, seen: now}
}

// KillOn reports the live kill flag and when control state was last seen.
// An outage returns the error the resume path reads as stale control.
func (c *controlPlane) KillOn() (bool, time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.reachable {
		return false, c.seen, errors.New("flags provider unreachable")
	}
	return c.kill, c.seen, nil
}

// SetKill flips the live kill flag.
func (c *controlPlane) SetKill(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kill = v
}

// SetReachable flips the provider outage.
func (c *controlPlane) SetReachable(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reachable = v
}

// invoiceJob is one external work item, composed offline: no workflow
// server delivers it, the fixture queue does.
type invoiceJob struct {
	OperationID string
	Invoice     string
	AmountCents int
}

// jobQueue is the external queue stand-in; Fetch removes one item.
type jobQueue struct {
	mu   sync.Mutex
	pend []invoiceJob
}

func newJobQueue(jobs ...invoiceJob) *jobQueue { return &jobQueue{pend: jobs} }

// Fetch delivers the next work item, or ok=false when the queue is empty.
func (q *jobQueue) Fetch() (invoiceJob, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pend) == 0 {
		return invoiceJob{}, false
	}
	j := q.pend[0]
	q.pend = q.pend[1:]
	return j, true
}

// outcome records what the composition did with one job.
type outcome struct {
	suspended bool
	reason    types.SuspendReason
	denied    bool
	executed  bool
}

// executed records the tools the composition actually ran, in order.
var executed []string

func recordExecution(tool string) { executed = append(executed, tool) }

// startWorkerRun claims a lease for one worker on the job's operation.
// The session and run ids carry the worker name, so two workers race on
// the same OperationID and the store decides the winner.
func startWorkerRun(ctx context.Context, runs *stores.MemoryRuns, job invoiceJob, worker string) (stores.Lease, stores.Run, error) {
	run := stores.Run{
		SessionID:   "invoice-" + job.OperationID + "-" + worker,
		RunID:       "run-" + job.OperationID + "-" + worker,
		RootRunID:   "run-" + job.OperationID,
		OperationID: job.OperationID,
		Flow:        "camunda-invoice",
		Backend:     "scripted",
		StartedAt:   time.Now(),
		Heartbeat:   time.Now(),
	}
	lease, err := runs.Start(ctx, run, stores.LeaseTTL)
	return lease, run, err
}

// startRun claims a lease for the job's operation. A second worker on the
// same OperationID loses here: the store rejects it.
func startRun(ctx context.Context, runs *stores.MemoryRuns, job invoiceJob) (stores.Lease, stores.Run, error) {
	run := stores.Run{
		SessionID:   "invoice-" + job.OperationID,
		RunID:       "run-" + job.OperationID,
		RootRunID:   "run-" + job.OperationID,
		OperationID: job.OperationID,
		Flow:        "camunda-invoice",
		Backend:     "scripted",
		StartedAt:   time.Now(),
		Heartbeat:   time.Now(),
	}
	lease, err := runs.Start(ctx, run, stores.LeaseTTL)
	return lease, run, err
}

// askApproval is the user task: the side-effecting call suspends the run
// for a human, and the pending item rides the resume token.
func askApproval(ctx context.Context, runs *stores.MemoryRuns, lease stores.Lease, token types.ResumeToken) error {
	return runs.Suspend(ctx, lease, token)
}

// resumeKind is what the resume path decides about the pending call.
type resumeKind int

const (
	resumeAllow resumeKind = iota
	resumeSuspendControl
	resumeDenyControlWait
	resumeDenyKill
	resumeDenyAuthority
)

// resume re-checks live state before the pending call executes. The
// checks run in order: control freshness, control wait, the live kill
// flag, then the originator's current scopes. A live denial wins even
// after an approval arrived.
func resume(ctx context.Context, cp *controlPlane, inv *permission.ToolInvocation, suspendedAt, now time.Time) (resumeKind, string) {
	if now.Sub(suspendedAt) > MaxControlWait {
		return resumeDenyControlWait, "control state unresolved past MaxControlWait"
	}
	if _, _, err := cp.KillOn(); err != nil {
		return resumeSuspendControl, "flags provider unreachable within FreshnessLimit"
	}
	if missing := missingScopes(inv.Run.Principal.Scopes, inv.Spec.RequiredScopes); len(missing) > 0 {
		return resumeDenyAuthority, "originator scope revoked while suspended"
	}
	d := permission.DecideCall(ctx, &killSwitchDecider{cp: cp}, inv)
	if d.Verdict == permission.DenyVerdict {
		return resumeDenyKill, d.Reason
	}
	return resumeAllow, ""
}

func missingScopes(have, want []string) []string {
	var missing []string
	for _, s := range want {
		found := false
		for _, h := range have {
			if h == s {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, s)
		}
	}
	return missing
}

// killSwitchDecider denies every call while the live kill flag is on, so
// the live verdict outranks an approval granted before it.
type killSwitchDecider struct {
	cp *controlPlane
}

func (d *killSwitchDecider) Decide(context.Context, *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	if kill, _, _ := d.cp.KillOn(); kill {
		return types.Decision[permission.Verdict]{
			Value:      permission.DenyVerdict,
			Confidence: 1,
		}, nil
	}
	return types.Decision[permission.Verdict]{
		Value:      permission.Allow,
		Confidence: 1,
	}, nil
}

// runInfo builds the gate's view of the run: the originator's current
// scopes, so a revocation while suspended denies on resume.
func runInfo(runID string, p types.Principal) types.RunInfo {
	return types.RunInfo{
		Flow:      "camunda-invoice",
		RunID:     runID,
		RootRunID: runID,
		Principal: p,
	}
}

func invoiceInvocation(job invoiceJob) *permission.ToolInvocation {
	spec := types.ToolSpec{
		Name:           "invoice.send",
		Description:    "send the invoice to the customer",
		Effect:         types.SideEffect,
		RequiredScopes: []string{sendScope},
	}
	return &permission.ToolInvocation{
		Spec: spec,
		Call: types.ToolUse{
			ID:   "call-" + job.OperationID,
			Name: spec.Name,
			Args: []byte(fmt.Sprintf(`{"invoice":%q,"amount_cents":%d}`, job.Invoice, job.AmountCents)),
		},
	}
}

func approverPrincipal(subject string, scopes ...string) types.Principal {
	return types.Principal{
		Tenant:  "acme",
		Subject: subject,
		Scopes:  scopes,
	}
}
