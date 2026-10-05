// Package main composes an offline invoice job: an external work item is
// fetched, a user task suspends the governed run for human approval, and
// the resume path re-checks live control state before any side effect
// runs. Memory stores and a fixed clock keep it offline: no network, no
// keys.
package main

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"iter"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
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
// An outage returns the error the flow's decider reads as stale control.
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

// outcome records what the governed composition did with one job.
type outcome struct {
	suspended bool
	reason    types.SuspendReason
	denied    bool
	executed  bool
}

// executed records the tools the batch actually ran, in order.
var executed []string

func recordExecution(tool string) { executed = append(executed, tool) }

// invoiceTool is the side-effecting fixture the scripted batch draws on.
// It records every execution so the tests can count real side effects.
type invoiceTool struct {
	mu    sync.Mutex
	calls []string
}

func (t *invoiceTool) Spec() types.ToolSpec {
	return types.ToolSpec{
		Name:           "invoice.send",
		Description:    "send the invoice to the customer",
		Effect:         types.SideEffect,
		RequiredScopes: []string{sendScope},
	}
}

func (t *invoiceTool) Call(_ context.Context, args jsontext.Value) (types.ToolResult, error) {
	var in invoiceJob
	_ = json.Unmarshal(args, &in)
	t.mu.Lock()
	t.calls = append(t.calls, in.Invoice)
	t.mu.Unlock()
	recordExecution(t.Spec().Name)
	return types.ToolResult{
		Content: []types.Block{types.Text{Text: "invoice sent"}},
		Outcome: types.Succeeded,
	}, nil
}

func (t *invoiceTool) ran() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.calls...)
}

// scriptedModel replays the flow: it calls invoice.send once per drive,
// each turn under a fresh call id, then finishes once the call is answered
// by an execution or a denial.
type scriptedModel struct {
	seq int
}

func (m *scriptedModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "scripted", Caps: types.Caps{Tools: true}}
}

func (m *scriptedModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				r, ok := b.(types.ToolResult)
				if !ok {
					continue
				}
				// A real execution answers "invoice sent"; a denial
				// carries an error. A delivered approval settles the
				// batch instead, so the call is re-issued on replay
				// and the decider's live verdict decides it.
				if r.Error != nil || resultText(r) == "invoice sent" {
					yield(types.ModelChunk{Kind: types.DeltaText, Delta: "invoice handled."}, nil)
					yield(types.ModelChunk{Finish: types.FinishStop}, nil)
					return
				}
			}
		}
		m.seq++
		yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{
			ID:   fmt.Sprintf("call-invoice.send-%d", m.seq),
			Name: "invoice.send",
			Args: jsontext.Value(`{"OperationID":"INV-2026-0042","Invoice":"INV-2026-0042","AmountCents":149500}`),
		}}, nil)
		yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
	}
}

func resultText(r types.ToolResult) string {
	for _, b := range r.Content {
		if t, ok := b.(types.Text); ok {
			return t.Text
		}
	}
	return ""
}

// approvalPolicy keeps the governed resume path permissive about who may
// approve; the flow's decider still owns the live verdict.
type approvalPolicy struct{}

func (approvalPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{}, nil
}

func approverPrincipal(subject string, scopes ...string) types.Principal {
	return types.Principal{
		Tenant:  "acme",
		Subject: subject,
		Scopes:  scopes,
	}
}
