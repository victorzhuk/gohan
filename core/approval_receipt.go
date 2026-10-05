package gohan

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const ApprovalReceiptKey = "gohan.approval"

type approvalReceipt struct {
	Version    int               `json:"version"`
	RunID      string            `json:"run_id"`
	Generation uint64            `json:"generation"`
	CallID     string            `json:"call_id"`
	Tool       string            `json:"tool"`
	Args       json.RawMessage   `json:"args"`
	Approvers  []types.Principal `json:"approvers"`
}

func receiptDedupKey(rc approvalReceipt) string {
	return rc.RunID + "\x00" + fmt.Sprint(rc.Generation) + "\x00" + rc.CallID
}

func approvalReceiptMessage(rc approvalReceipt) (types.Message, error) {
	rc.Version = checkpointEnvelopeVersion
	if rc.RunID == "" || rc.CallID == "" || rc.Tool == "" || len(rc.Args) == 0 {
		return types.Message{}, fmt.Errorf("%w: incomplete approval receipt", types.ErrInputInvalid)
	}
	return types.Message{Role: types.RoleAssistant, Meta: map[string]any{ApprovalReceiptKey: rc}}, nil
}

func rejectReservedMeta(msgs []types.Message) error {
	for _, msg := range msgs {
		if _, ok := msg.Meta[ApprovalReceiptKey]; ok {
			return fmt.Errorf("%w: message carries the reserved %s metadata", types.ErrInputInvalid, ApprovalReceiptKey)
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
	if rc.Version != checkpointEnvelopeVersion {
		return approvalReceipt{}, fmt.Errorf("%w: unsupported approval receipt version %d", types.ErrInputInvalid, rc.Version)
	}
	if rc.RunID == "" || rc.CallID == "" || rc.Tool == "" || len(rc.Args) == 0 {
		return approvalReceipt{}, fmt.Errorf("%w: incomplete approval receipt", types.ErrInputInvalid)
	}
	return rc, nil
}

// latestApprovedArgs returns the arguments of the latest valid approval
// receipt for tool in the session history, or nil when none exists. It
// decodes Meta through JSON so a persisted map and an in-memory struct
// behave identically; a malformed reserved receipt is an error.
func latestApprovedArgs(h stores.History, tool string) (json.RawMessage, error) {
	var latest approvalReceipt
	found := false
	for _, msg := range h.Messages {
		value, ok := msg.Meta[ApprovalReceiptKey]
		if !ok {
			continue
		}
		rc, err := decodeReceipt(value)
		if err != nil {
			return nil, err
		}
		if rc.Tool != tool {
			continue
		}
		latest = rc
		found = true
	}
	if !found {
		return nil, nil
	}
	return latest.Args, nil
}
