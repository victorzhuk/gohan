package gohan

import (
	"context"
	"errors"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// SessionControlWriter is the optional per-session control write on a
// session log. A log without it refuses takeover and hand-back, since the
// control state would drift from the store the operator queue reads.
type SessionControlWriter interface {
	SetControl(ctx context.Context, sessionID string, control stores.SessionControl) error
}

// ApprovalExpirer resolves one pending human-approval token. The expiry
// queue lives above core; it is injected per call, never imported.
type ApprovalExpirer interface {
	Expire(token types.ResumeToken) bool
}

type approvalExpiryCtxKey struct{}

// WithApprovalExpiry binds the expirer TakeOver resolves pending approval
// tokens against.
func WithApprovalExpiry(ctx context.Context, e ApprovalExpirer) context.Context {
	return context.WithValue(ctx, approvalExpiryCtxKey{}, e)
}

func approvalExpiryFrom(ctx context.Context) (ApprovalExpirer, bool) {
	e, ok := ctx.Value(approvalExpiryCtxKey{}).(ApprovalExpirer)
	return e, ok
}

type decisionAuditCtxKey struct{}

// WithDecisionAudit binds the audit trail the control transitions record
// into. Only the harness holds the audit log, so it travels in context.
func WithDecisionAudit(ctx context.Context, a stores.AuditLog) context.Context {
	return context.WithValue(ctx, decisionAuditCtxKey{}, a)
}

func decisionAuditFrom(ctx context.Context) (stores.AuditLog, bool) {
	a, ok := ctx.Value(decisionAuditCtxKey{}).(stores.AuditLog)
	return a, ok
}

var (
	errControlWriterMissing = errors.New("gohan: session log does not support control writes")
	errApprovalExpiryNeeded = errors.New("gohan: takeover of a pending approval needs an expiry binding")
)

// Audit kinds and decisions for the control transitions; the floor names
// none, because the transitions are the conversation's own vocabulary.
const (
	auditHandoffAccepted stores.AuditKind = "handoff_accepted"
	auditHandback        stores.AuditKind = "handback"

	decisionControlHuman = "control_human"
	decisionControlAgent = "control_agent"
)

// TakeOver pauses the agent: the live run is cancelled at its next safe
// point, a pending human-approval token is expired with the handoff
// verdict, control moves to the operator and the transition lands in the
// audit trail under the operator's subject.
func (c *conversation) TakeOver(ctx context.Context, sessionID string, token types.ResumeToken, operator types.Principal) error {
	if operator.Subject == "" {
		return types.ErrNoPrincipal
	}
	if err := c.checkControlOperator(ctx, sessionID, operator); err != nil {
		return err
	}
	run, live := c.find(ctx, sessionID)
	if live {
		if err := c.runs.Signal(ctx, run.RunID, stores.Signal{Kind: stores.SignalCancel}); err != nil {
			return err
		}
	}
	if token != "" {
		exp, ok := approvalExpiryFrom(ctx)
		if !ok {
			return errApprovalExpiryNeeded
		}
		exp.Expire(token)
	}
	if err := c.setControl(ctx, sessionID, stores.ControlHuman); err != nil {
		return err
	}
	if err := c.auditControl(ctx, sessionID, run.RunID, operator, auditHandoffAccepted, decisionControlHuman); err != nil {
		return err
	}
	if live {
		c.waitForStop(ctx, sessionID)
	}
	return nil
}

// HandBack returns control to the agent. Restoring the fenced operator
// turns into the model request is M1-deferred; this chunk only moves the
// control state back and audits it.
func (c *conversation) HandBack(ctx context.Context, sessionID string, operator types.Principal) error {
	if operator.Subject == "" {
		return types.ErrNoPrincipal
	}
	if err := c.setControl(ctx, sessionID, stores.ControlAgent); err != nil {
		return err
	}
	return c.auditControl(ctx, sessionID, "", operator, auditHandback, decisionControlAgent)
}

// OperatorSend appends the operator's message as an assistant turn with
// OriginOperator and returns it to attached clients. No model call is
// made: the agent is paused under human control.
func (c *conversation) OperatorSend(ctx context.Context, sessionID string, operator types.Principal, msg Message) (Event, error) {
	if operator.Subject == "" {
		return nil, types.ErrNoPrincipal
	}
	msg.Role = types.RoleAssistant
	msg.Blocks = operatorBlocks(msg.Blocks, operator.Subject)
	h, err := c.load(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if _, aerr := c.log.Append(ctx, sessionID, h.Version, msg); aerr != nil {
		return nil, aerr
	}
	return AssistantMessage{Message: msg, Operator: true}, nil
}

// operatorBlocks stamps every origin-bearing block with OriginOperator.
// Blob carries no origin, so it passes through untouched.
func operatorBlocks(blocks []Block, subject string) []Block {
	origin := types.Origin{Kind: types.OriginOperator, Name: subject}
	out := make([]Block, len(blocks))
	for i, b := range blocks {
		switch v := b.(type) {
		case types.Text:
			v.Origin = origin
			out[i] = v
		case types.Reasoning:
			v.Origin = origin
			out[i] = v
		case types.Image:
			v.Origin = origin
			out[i] = v
		default:
			out[i] = b
		}
	}
	return out
}

func (c *conversation) setControl(ctx context.Context, sessionID string, control stores.SessionControl) error {
	w, ok := c.log.(SessionControlWriter)
	if !ok {
		return errControlWriterMissing
	}
	return w.SetControl(ctx, sessionID, control)
}

func (c *conversation) auditControl(ctx context.Context, sessionID, runID string, operator types.Principal, kind stores.AuditKind, decision string) error {
	a, ok := decisionAuditFrom(ctx)
	if !ok {
		return nil
	}
	return a.Append(ctx, stores.AuditRecord{
		Kind:      kind,
		SessionID: sessionID,
		RunID:     runID,
		Tenant:    operator.Tenant,
		Approver:  operator.Subject,
		Decision:  decision,
	})
}

// waitForStop blocks until the runs store releases the session's lease or
// the lease TTL elapses, mirroring Cancel's bounded wait.
func (c *conversation) waitForStop(ctx context.Context, sessionID string) {
	h, ok := c.runs.(stores.SessionLeaseHolder)
	if !ok {
		return
	}
	deadline := time.Now().Add(stores.LeaseTTL)
	for h.SessionLeaseActive(ctx, sessionID) {
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
