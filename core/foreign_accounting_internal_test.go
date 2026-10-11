package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestValidateForeignProgress(t *testing.T) {
	if err := validateForeignProgress(nil); err != nil {
		t.Fatalf("nil progress: %v", err)
	}
	valid := &runtime.ForeignProgress{Version: foreignProgressVersion, AdmittedTotal: 2, ReplayCallIDs: []string{"call-1", "call-2"}}
	if err := validateForeignProgress(valid); err != nil {
		t.Fatalf("valid progress: %v", err)
	}
	cases := map[string]*runtime.ForeignProgress{
		"unknown version": {Version: 99, AdmittedTotal: 1, ReplayCallIDs: []string{"call-1"}},
		"negative total":  {Version: 1, AdmittedTotal: -1},
		"empty id":        {Version: 1, AdmittedTotal: 1, ReplayCallIDs: []string{""}},
		"duplicate id":    {Version: 1, AdmittedTotal: 2, ReplayCallIDs: []string{"call-1", "call-1"}},
		"total mismatch":  {Version: 1, AdmittedTotal: 3, ReplayCallIDs: []string{"call-1", "call-2"}},
		"count mismatch":  {Version: 1, AdmittedTotal: 1, ReplayCallIDs: []string{"call-1", "call-2"}},
	}
	for name, p := range cases {
		if err := validateForeignProgress(p); !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("%s: err = %v, want ErrCheckpointIncompatible", name, err)
		}
	}
}

func TestForeignAdmissionRecordRoundTrip(t *testing.T) {
	msg, err := foreignAdmissionMessage(foreignAdmission{RunID: "r1", AdmittedTotal: 1, CallID: "call-1"})
	if err != nil {
		t.Fatalf("message: %v", err)
	}
	persisted, err := json.Marshal(msg.Meta[foreignAdmissionKey])
	if err != nil {
		t.Fatal(err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(persisted, &asMap); err != nil {
		t.Fatal(err)
	}
	rec, err := decodeForeignAdmission(asMap)
	if err != nil {
		t.Fatalf("decode persisted: %v", err)
	}
	if rec.RunID != "r1" || rec.CallID != "call-1" || rec.AdmittedTotal != 1 || rec.Version != foreignProgressVersion {
		t.Fatalf("record = %+v, want the r1/call-1 admission", rec)
	}
	if _, err := decodeForeignAdmission(map[string]any{"version": 7, "run_id": "r1", "admitted_total": 1, "call_id": "c"}); !errors.Is(err, types.ErrCheckpointIncompatible) {
		t.Fatalf("version refusal = %v, want ErrCheckpointIncompatible", err)
	}
	if _, err := decodeForeignAdmission(map[string]any{"version": 1, "run_id": "r1"}); !errors.Is(err, types.ErrCheckpointIncompatible) {
		t.Fatalf("incomplete refusal = %v, want ErrCheckpointIncompatible", err)
	}
}

func TestForeignRunStateMerge(t *testing.T) {
	fs := newForeignRunState("r1", 5)
	if err := fs.restore(&runtime.ForeignProgress{Version: 1, AdmittedTotal: 2, ReplayCallIDs: []string{"call-1", "call-2"}}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !fs.admitted("call-1") || fs.totalCalls() != 2 || fs.currentVersion() != 5 {
		t.Fatalf("state = total %d ver %d, want total 2 ver 5", fs.totalCalls(), fs.currentVersion())
	}
	// A re-read of an older scan is idempotent; a gap is refused.
	if err := fs.admit("call-1", 1); err != nil {
		t.Fatalf("idempotent re-read: %v", err)
	}
	if err := fs.admit("call-4", 4); !errors.Is(err, types.ErrCheckpointIncompatible) {
		t.Fatalf("gap refusal = %v, want ErrCheckpointIncompatible", err)
	}
	if err := fs.admit("call-3", 3); err != nil {
		t.Fatalf("next admission: %v", err)
	}
	p := fs.progress()
	if p == nil || p.AdmittedTotal != 3 || len(p.ReplayCallIDs) != 3 {
		t.Fatalf("progress = %+v, want total 3 with three identities", p)
	}
	// An older checkpoint total never rolls the restored state back.
	if err := fs.restore(&runtime.ForeignProgress{Version: 1, AdmittedTotal: 1, ReplayCallIDs: []string{"call-1"}}); !errors.Is(err, types.ErrCheckpointIncompatible) {
		t.Fatalf("older checkpoint = %v, want ErrCheckpointIncompatible", err)
	}
}

func TestScanForeignAdmissionsRunScoped(t *testing.T) {
	fs := newForeignRunState("r1", 0)
	msg, err := foreignAdmissionMessage(foreignAdmission{RunID: "r2", AdmittedTotal: 1, CallID: "other-run"})
	if err != nil {
		t.Fatal(err)
	}
	if err := scanForeignAdmissions(fs, stores.History{Messages: []types.Message{msg}}); err != nil {
		t.Fatalf("other-run record: %v", err)
	}
	if fs.totalCalls() != 0 {
		t.Fatalf("total = %d, want another run's records ignored", fs.totalCalls())
	}
	m1, _ := foreignAdmissionMessage(foreignAdmission{RunID: "r1", AdmittedTotal: 1, CallID: "call-1"})
	m2, _ := foreignAdmissionMessage(foreignAdmission{RunID: "r1", AdmittedTotal: 2, CallID: "call-2"})
	if err := scanForeignAdmissions(fs, stores.History{Messages: []types.Message{m1, m2}}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if fs.totalCalls() != 2 || !fs.admitted("call-2") {
		t.Fatalf("state total = %d, want 2 with call-2 admitted", fs.totalCalls())
	}
	bad, _ := foreignAdmissionMessage(foreignAdmission{RunID: "r1", AdmittedTotal: 9, CallID: "call-9"})
	if err := scanForeignAdmissions(fs, stores.History{Messages: []types.Message{bad}}); !errors.Is(err, types.ErrCheckpointIncompatible) {
		t.Fatalf("non-monotonic scan = %v, want ErrCheckpointIncompatible", err)
	}
}

func TestReplayReservationPrepaysWithoutCharging(t *testing.T) {
	ls := chains.NewLimitsStateSeeded(0)
	if _, _, err := ls.ReserveBatch(context.Background(), types.RunLimits{MaxToolCalls: 2}, 1); err != nil {
		t.Fatalf("ReserveBatch: %v", err)
	}
	if got := ls.Snapshot().ToolUses; got != 1 {
		t.Fatalf("toolUses = %d, want 1", got)
	}
	rctx := ls.ReplayReservation(context.Background(), 1)
	if !ls.ConsumeReservation(rctx) {
		t.Fatal("replay slot not consumable")
	}
	if got := ls.Snapshot().ToolUses; got != 1 {
		t.Fatalf("toolUses = %d after replay, want the charge unchanged at 1", got)
	}
	if ls.ConsumeReservation(rctx) {
		t.Fatal("second consume on a one-slot replay reservation succeeded")
	}
}
