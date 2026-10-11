package gohan

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// auditGrantedScope is the audit event a stored scope grant produces. The
// name is the shared contract with std/permission.GrantedByScope; core
// holds the literal, not a policy constant.
const auditGrantedScope = "granted_by_scope"

// storeScopedGrants persists the session grants one scoped approval
// produced: one grant per approval-controlled call that reached its
// quorum, keyed by session, tool, fingerprint and the approving subject.
// The expiry derives from the actual approval instant, never from the
// recorded request expiry; the TTL cap is the grant store's policy. The
// grant and its granted_by_scope audit record land before the approved
// call executes, and a failure fails the resume closed.
func storeScopedGrants(ctx context.Context, store permission.GrantStore, audit stores.AuditLog, sessionID, runID, flow string, originator, approver types.Principal, env checkpointEnvelope, ttl time.Duration, at time.Time) error {
	if store == nil {
		return fmt.Errorf("scoped approval: no grant store wired: %w", types.ErrInputInvalid)
	}
	for _, ap := range env.Approvals {
		if len(ap.ApprovedBy) == 0 {
			continue
		}
		g := permission.Grant{
			Tool:        ap.Call.Name,
			Fingerprint: ap.Fingerprint,
			Subject:     approver.Subject,
			Approver:    approver.Subject,
			ExpiresAt:   at.Add(ttl),
		}
		if err := store.Put(ctx, sessionID, g); err != nil {
			return fmt.Errorf("store scoped grant: %w", err)
		}
		if audit == nil {
			continue
		}
		reason, err := json.Marshal(map[string]string{
			"ttl":         ttl.String(),
			"fingerprint": string(ap.Fingerprint),
			"expires_at":  g.ExpiresAt.UTC().Format(time.RFC3339Nano),
		})
		if err != nil {
			return fmt.Errorf("encode scoped grant audit: %w", err)
		}
		rec := stores.AuditRecord{
			Kind:      stores.AuditKind(auditGrantedScope),
			At:        at,
			SessionID: sessionID,
			RunID:     runID,
			RootRunID: env.Run.RootRunID,
			Flow:      flow,
			Subject:   originator.Subject,
			Tenant:    originator.Tenant,
			Tool:      g.Tool,
			Approver:  approver.Subject,
			Decision:  "grant",
			Reason:    string(reason),
		}
		if err := audit.Append(ctx, rec); err != nil {
			return fmt.Errorf("audit scoped grant: %w", err)
		}
	}
	return nil
}

// approvedCall is one checkpointed pending call a resume approved, pinned
// by both its id and its fingerprint so an edited or reissued call never
// inherits the decision.
type approvedCall struct {
	id          string
	fingerprint stores.Fingerprint
}

type approvedCallKey struct{}

// withApprovedCalls marks the pending calls this resume approved. The
// marker rides the resumed drive's context and never leaves it: a resumed
// turn may replay the parked call, and nothing else.
func withApprovedCalls(ctx context.Context, calls map[approvedCall]bool) context.Context {
	if len(calls) == 0 {
		return ctx
	}
	return context.WithValue(ctx, approvedCallKey{}, calls)
}

// approvedCalls collects the pending calls the recorded approvals name, so
// the replay of one decided ask is the only call the approving subject's
// grant may serve.
func approvedCalls(env checkpointEnvelope, approver types.Principal) map[approvedCall]bool {
	var calls map[approvedCall]bool
	for _, ap := range env.Approvals {
		if len(ap.ApprovedBy) == 0 {
			continue
		}
		if calls == nil {
			calls = make(map[approvedCall]bool, len(env.Approvals))
		}
		calls[approvedCall{id: ap.Call.ID, fingerprint: ap.Fingerprint}] = true
	}
	if len(calls) == 0 {
		return nil
	}
	if approver.Subject == "" {
		return nil
	}
	return calls
}

// approvedCallReplayed reports whether the invocation is the parked call
// this resume approved, matched on call id and fingerprint.
func approvedCallReplayed(ctx context.Context, inv *permission.ToolInvocation) bool {
	calls, _ := ctx.Value(approvedCallKey{}).(map[approvedCall]bool)
	if len(calls) == 0 {
		return false
	}
	return calls[approvedCall{
		id:          inv.Call.ID,
		fingerprint: ToolFingerprint(inv.Spec, inv.Call.Args),
	}]
}

// grantSubject resolves the subject a grant must be keyed on. The parked
// call the resume approved replays under the approving subject the resume
// installed, so it finds its own grant: that authority is limited to the
// persisted decision, id and fingerprint together. Every other call is a
// fresh one and is authorized under its own run principal — the ambient
// approval a resume left in context never speaks for it, so a resumed
// turn's second call asks again unless the originator holds a grant of
// its own. A principal that cannot be read grants nothing.
func grantSubject(ctx context.Context, inv *permission.ToolInvocation) string {
	if ap, ok := types.ApprovalFrom(ctx); ok && ap.Approver.Subject != "" && approvedCallReplayed(ctx, inv) {
		return ap.Approver.Subject
	}
	return inv.Run.Principal.Subject
}

// scopeGrantCheck builds the store-backed grant check the native and
// foreign gates consult after the receipt check: a live grant covering the
// invocation's fingerprint under the resuming approver's subject passes
// the call, and the same call under the originator's principal passes on a
// later drive. A context carrying neither grants nothing, so a scoped
// receipt alone can never bypass the subject, TTL or new-call enforcement.
func scopeGrantCheck(store permission.GrantStore) func(context.Context, *permission.ToolInvocation) bool {
	return func(ctx context.Context, inv *permission.ToolInvocation) bool {
		if store == nil {
			return false
		}
		subject := grantSubject(ctx, inv)
		if subject == "" {
			return false
		}
		ri, ok := types.RunInfoFrom(ctx)
		if !ok || ri.SessionID == "" {
			return false
		}
		fp := ToolFingerprint(inv.Spec, inv.Call.Args)
		_, found, err := store.Find(ctx, ri.SessionID, inv.Spec.Name, fp, subject)
		return err == nil && found
	}
}
