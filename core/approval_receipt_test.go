package gohan

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func testReceipt(tool string) approvalReceipt {
	return approvalReceipt{
		Version:    1,
		RunID:      "run-1",
		Generation: 1,
		CallID:     "c1",
		Tool:       tool,
		Args:       json.RawMessage(`{"a":1}`),
		Approvers:  []types.Principal{{Subject: "fin", Tenant: "t1"}},
	}
}

func TestApprovalReceiptRoundTrip(t *testing.T) {
	rc := testReceipt("book")
	msg, err := approvalReceiptMessage(rc)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Role != types.RoleAssistant {
		t.Fatalf("role = %s, want assistant", msg.Role)
	}
	if !isReceiptMessage(msg) {
		t.Fatal("receipt message not recognized by isReceiptMessage")
	}
	if isReceiptMessage(types.Message{Role: types.RoleUser}) {
		t.Fatal("plain message recognized as receipt")
	}

	persisted, err := json.Marshal(msg.Meta[ApprovalReceiptKey])
	if err != nil {
		t.Fatal(err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(persisted, &asMap); err != nil {
		t.Fatal(err)
	}

	h := stores.History{Messages: []types.Message{
		{Role: types.RoleAssistant, Meta: map[string]any{ApprovalReceiptKey: asMap}},
	}}
	got, err := latestApprovedArgs(h, "book")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":1}` {
		t.Fatalf("args = %s, want {\"a\":1}", got)
	}

	h = stores.History{Messages: []types.Message{
		{Role: types.RoleAssistant, Meta: map[string]any{ApprovalReceiptKey: rc}},
	}}
	got, err = latestApprovedArgs(h, "book")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":1}` {
		t.Fatalf("struct-valued receipt: args = %s", got)
	}
}

func TestRejectReservedMeta(t *testing.T) {
	if err := rejectReservedMeta([]types.Message{{Role: types.RoleUser, Meta: map[string]any{ApprovalReceiptKey: map[string]any{}}}}); !errors.Is(err, types.ErrInputInvalid) {
		t.Fatalf("err = %v, want ErrInputInvalid", err)
	}
	if err := rejectReservedMeta([]types.Message{{Role: types.RoleUser, Meta: map[string]any{"other": 1}}}); err != nil {
		t.Fatalf("unrelated metadata rejected: %v", err)
	}
}

func TestLatestApprovedArgs(t *testing.T) {
	t.Run("latest-per-tool-selected", func(t *testing.T) {
		first := testReceipt("book")
		second := testReceipt("book")
		second.CallID = "c2"
		second.Args = json.RawMessage(`{"a":2}`)
		other := testReceipt("fetch")
		h := stores.History{Messages: []types.Message{
			{Role: types.RoleAssistant, Meta: map[string]any{ApprovalReceiptKey: first}},
			{Role: types.RoleAssistant, Meta: map[string]any{ApprovalReceiptKey: other}},
			{Role: types.RoleAssistant, Meta: map[string]any{ApprovalReceiptKey: second}},
		}}
		got, err := latestApprovedArgs(h, "book")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `{"a":2}` {
			t.Fatalf("args = %s, want the latest book receipt", got)
		}
	})

	t.Run("no-predecessor-nil", func(t *testing.T) {
		got, err := latestApprovedArgs(stores.History{}, "book")
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("args = %s, want nil", got)
		}
	})

	t.Run("malformed-receipt-errors", func(t *testing.T) {
		h := stores.History{Messages: []types.Message{
			{Role: types.RoleAssistant, Meta: map[string]any{ApprovalReceiptKey: "not-a-receipt"}},
		}}
		if _, err := latestApprovedArgs(h, "book"); !errors.Is(err, types.ErrInputInvalid) {
			t.Fatalf("err = %v, want ErrInputInvalid", err)
		}
	})

	t.Run("incomplete-receipt-errors", func(t *testing.T) {
		h := stores.History{Messages: []types.Message{
			{Role: types.RoleAssistant, Meta: map[string]any{ApprovalReceiptKey: map[string]any{"version": 1, "tool": "book"}}},
		}}
		if _, err := latestApprovedArgs(h, "book"); !errors.Is(err, types.ErrInputInvalid) {
			t.Fatalf("err = %v, want ErrInputInvalid", err)
		}
	})

	t.Run("mismatched-tool-ignored", func(t *testing.T) {
		h := stores.History{Messages: []types.Message{
			{Role: types.RoleAssistant, Meta: map[string]any{ApprovalReceiptKey: testReceipt("fetch")}},
		}}
		got, err := latestApprovedArgs(h, "book")
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("args = %s, want nil", got)
		}
	})
}

func TestReceiptDedupKey(t *testing.T) {
	a := testReceipt("book")
	b := a
	if receiptDedupKey(a) != receiptDedupKey(b) {
		t.Fatal("same identity produced different dedup keys")
	}
	b.Generation = 2
	if receiptDedupKey(a) == receiptDedupKey(b) {
		t.Fatal("different generations share a dedup key")
	}
}
