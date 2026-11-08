package gohan

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const ApprovalReceiptKey = "gohan.approval"

// approvalReceiptVersion is independent of the checkpoint envelope
// version: receipts already recorded in session history must keep
// decoding while the envelope format moves on.
const approvalReceiptVersion = 1

type approvalReceipt struct {
	Version    int               `json:"version"`
	RunID      string            `json:"run_id"`
	Generation uint64            `json:"generation"`
	CallID     string            `json:"call_id"`
	Tool       string            `json:"tool"`
	Args       json.RawMessage   `json:"args"`
	Approvers  []types.Principal `json:"approvers"`
	// Scoped marks a receipt a scoped approval produced. A scoped receipt
	// never grants through the receipt path: its authority lives in the
	// grant store, keyed by the approving subject.
	Scoped bool `json:"scoped,omitempty"`
}

func receiptDedupKey(rc approvalReceipt) string {
	return rc.RunID + "\x00" + fmt.Sprint(rc.Generation) + "\x00" + rc.CallID
}

func approvalReceiptMessage(rc approvalReceipt) (types.Message, error) {
	rc.Version = approvalReceiptVersion
	if rc.RunID == "" || rc.CallID == "" || rc.Tool == "" || len(rc.Args) == 0 {
		return types.Message{}, fmt.Errorf("%w: incomplete approval receipt", types.ErrInputInvalid)
	}
	return types.Message{Role: types.RoleAssistant, Meta: map[string]any{ApprovalReceiptKey: rc}}, nil
}

// receiptGrantCheck builds the session-grant lookup the permission gate
// consults after the taint policy and before the decider: the approval
// receipt recorded at quorum authorizes only the call identity it
// recorded — run, argument generation, call id, tool and the complete
// argument value — so the parked call replays on a resumed or recovered
// drive without asking while any later call asks for its own decision. A
// malformed receipt is treated as no grant; a live deny from the decider
// runs after this check and always wins.
func receiptGrantCheck(h stores.History) func(context.Context, *permission.ToolInvocation) bool {
	return func(ctx context.Context, inv *permission.ToolInvocation) bool {
		rc, found, err := latestApprovedReceipt(h, inv.Spec.Name)
		if err != nil || !found {
			return false
		}
		if rc.RunID != inv.Run.RunID || rc.CallID != inv.Call.ID {
			return false
		}
		if fullArgsFingerprint(inv.Spec.Name, rc.Args) != fullArgsFingerprint(inv.Spec.Name, inv.Call.Args) {
			return false
		}
		if entry, replayed := approvedReplay(ctx, inv); replayed && entry.generation != rc.Generation {
			return false
		}
		return true
	}
}

func rejectReservedMeta(msgs []types.Message) error {
	for _, msg := range msgs {
		if _, ok := msg.Meta[ApprovalReceiptKey]; ok {
			return fmt.Errorf("%w: message carries the reserved %s metadata", types.ErrInputInvalid, ApprovalReceiptKey)
		}
		if _, ok := msg.Meta[foreignAdmissionKey]; ok {
			return fmt.Errorf("%w: message carries the reserved %s metadata", types.ErrInputInvalid, foreignAdmissionKey)
		}
	}
	return nil
}

func isReceiptMessage(msg types.Message) bool {
	if _, ok := msg.Meta[ApprovalReceiptKey]; !ok {
		return false
	}
	_, err := decodeReceipt(msg.Meta[ApprovalReceiptKey])
	return err == nil
}

func decodeReceipt(value any) (approvalReceipt, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return approvalReceipt{}, fmt.Errorf("%w: unreadable approval receipt", types.ErrInputInvalid)
	}
	var rc approvalReceipt
	if err := jsonv2.Unmarshal(raw, &rc); err != nil {
		return approvalReceipt{}, fmt.Errorf("%w: malformed approval receipt: %s", types.ErrInputInvalid, err)
	}
	if rc.Version != approvalReceiptVersion {
		return approvalReceipt{}, fmt.Errorf("%w: unsupported approval receipt version %d", types.ErrInputInvalid, rc.Version)
	}
	if rc.RunID == "" || rc.CallID == "" || rc.Tool == "" || len(rc.Args) == 0 {
		return approvalReceipt{}, fmt.Errorf("%w: incomplete approval receipt", types.ErrInputInvalid)
	}
	if len(rc.Approvers) == 0 {
		return approvalReceipt{}, fmt.Errorf("%w: approval receipt without approvers", types.ErrInputInvalid)
	}
	return rc, nil
}

// appendReceiptMessages records rcpts that the session history does not
// already carry, keyed by run, generation and call id, and returns the
// version the append landed at. Both the resume and the recovery path
// record receipts through it, so a recovered drive never appends a second
// receipt for a key the resume already wrote. The history comes in loaded
// so the caller's version stays the expected append version.
func appendReceiptMessages(ctx context.Context, log stores.SessionLog, sessionID string, h stores.History, rcpts []approvalReceipt) (int64, error) {
	if len(rcpts) == 0 {
		return h.Version, nil
	}
	seen := map[string]bool{}
	for _, msg := range h.Messages {
		if raw, ok := msg.Meta[ApprovalReceiptKey]; ok {
			rc, err := decodeReceipt(raw)
			if err != nil {
				return 0, err
			}
			seen[receiptDedupKey(rc)] = true
		}
	}
	var msgs []types.Message
	for _, rc := range rcpts {
		if seen[receiptDedupKey(rc)] {
			continue
		}
		msg, err := approvalReceiptMessage(rc)
		if err != nil {
			return 0, err
		}
		msgs = append(msgs, msg)
	}
	if len(msgs) == 0 {
		return h.Version, nil
	}
	return log.Append(ctx, sessionID, h.Version, msgs...)
}

// latestApprovedReceipt returns the latest valid non-scoped approval
// receipt for tool in the session history. It decodes Meta through JSON
// so a persisted map and an in-memory struct behave identically; a
// malformed reserved receipt is an error.
func latestApprovedReceipt(h stores.History, tool string) (approvalReceipt, bool, error) {
	var latest approvalReceipt
	found := false
	for _, msg := range h.Messages {
		value, ok := msg.Meta[ApprovalReceiptKey]
		if !ok {
			continue
		}
		rc, err := decodeReceipt(value)
		if err != nil {
			return approvalReceipt{}, false, err
		}
		if rc.Scoped || rc.Tool != tool {
			continue
		}
		latest = rc
		found = true
	}
	return latest, found, nil
}

// latestApprovedArgs returns the arguments of the latest valid approval
// receipt for tool in the session history, or nil when none exists. A
// scoped receipt never grants through this path: its authority lives in
// the grant store.
func latestApprovedArgs(h stores.History, tool string) (json.RawMessage, error) {
	rc, found, err := latestApprovedReceipt(h, tool)
	if err != nil || !found {
		return nil, err
	}
	return rc.Args, nil
}
