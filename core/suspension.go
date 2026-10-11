package gohan

import (
	"encoding/json"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
)

// ResumeInput constructors over stores.ResumeInput. The approver is not a
// constructor argument: Resume sets it from the verified transport
// principal (identity/spec.md rule 5).

// Approve approves every pending call the run suspended on.
func Approve() stores.ResumeInput {
	return stores.ResumeInput{Verdict: stores.VerdictApprove}
}

// ApproveScope approves every pending call and asks for a session grant
// whose lifetime is ttl. The grant is keyed by session, tool, fingerprint
// and the verified approving subject; the grant store's policy caps the
// TTL and the gate derives the expiry from the actual approval instant.
// The grant and its granted_by_scope audit record are stored before the
// approved call executes; a store failure fails the resume closed.
func ApproveScope(ttl time.Duration) stores.ResumeInput {
	return stores.ResumeInput{Verdict: stores.VerdictApprove, ScopeTTL: ttl}
}

// Reject rejects the pending calls with the reason the model sees as the
// tool error result.
func Reject(reason string) stores.ResumeInput {
	return stores.ResumeInput{Verdict: stores.VerdictReject, Reason: reason}
}

// EditArgs replaces the pending calls' arguments before they execute.
func EditArgs(args json.RawMessage) stores.ResumeInput {
	return stores.ResumeInput{Verdict: stores.VerdictEdit, Args: args}
}

// Deliver completes an AwaitingTool or AwaitingInput suspension: data
// becomes the pending call's tool result.
func Deliver(data json.RawMessage) stores.ResumeInput {
	return stores.ResumeInput{Data: data}
}

// Continue resumes a Preempted run without a decision; it is valid only
// for that reason.
func Continue() stores.ResumeInput {
	return stores.ResumeInput{}
}
