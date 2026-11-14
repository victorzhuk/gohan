package notes

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/taint"
)

func runInfo(session string) types.RunInfo {
	return types.RunInfo{
		SessionID: session,
		RunID:     "r-" + session,
		Principal: types.Principal{Subject: "u1", Tenant: "acme"},
	}
}

func TestNotesToolContract(t *testing.T) {
	store := &stores.MemoryNotes{}
	n, err := New(store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Run("spec names notes_write in the session slot", func(t *testing.T) {
		spec := n.Spec()
		if spec.Name != "notes_write" {
			t.Fatalf("tool name = %q, want notes_write", spec.Name)
		}
		if spec.Effect != types.ReadOnly {
			t.Fatalf("effect = %d, want ReadOnly", spec.Effect)
		}
		if n.Slot() != types.SlotSession {
			t.Fatalf("slot = %d, want SlotSession", n.Slot())
		}
	})

	t.Run("conflicting version fails and keeps the store", func(t *testing.T) {
		ri := runInfo("s-cas")
		writeNote(t, n, ri, "first")
		stale, err := json.Marshal(writeArgs{Notes: "stale", ExpectedVersion: 1})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		writeNote(t, n, ri, "second")
		ctx := types.WithRunInfo(context.Background(), ri)
		res, err := n.Call(ctx, stale)
		if err != nil {
			if res.Outcome != types.Succeeded {
				t.Fatalf("stale write outcome = %d with error", res.Outcome)
			}
		} else if res.Outcome != types.Failed {
			t.Fatalf("stale write outcome = %d, want failed", res.Outcome)
		}
		if got := slotText(t, n, ri); got != "second" {
			t.Fatalf("notes after stale write = %q, want %q", got, "second")
		}
	})

	t.Run("sessions stay isolated", func(t *testing.T) {
		writeNote(t, n, runInfo("s-a"), "alpha")
		writeNote(t, n, runInfo("s-b"), "beta")
		if got := slotText(t, n, runInfo("s-a")); got != "alpha" {
			t.Fatalf("session a slot = %q, want alpha", got)
		}
		if got := slotText(t, n, runInfo("s-b")); got != "beta" {
			t.Fatalf("session b slot = %q, want beta", got)
		}
	})

	t.Run("taint.notes-readback-carries-origin", func(t *testing.T) {
		ri := runInfo("s-taint")
		const note = "secret note content"
		writeNote(t, n, ri, note)
		blocks, err := n.Provide(context.Background(), ri)
		if err != nil {
			t.Fatalf("Provide: %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("block count = %d, want 1", len(blocks))
		}
		if got := blocks[0].BlockOrigin(); got != (types.Origin{Kind: types.OriginTool, Name: "notes"}) {
			t.Fatalf("block origin = %+v, want OriginTool{notes}", got)
		}

		hook, err := taint.New(taint.TaintPolicy{
			ToExfil:      types.TaintDeny,
			ToSideEffect: types.TaintDeny,
			ToReadOnly:   types.TaintDeny,
			MinMatch:     1,
		}, nil)
		if err != nil {
			t.Fatalf("taint.New: %v", err)
		}
		ctx := types.WithTaintWindow(context.Background(), blocks)
		args := jsontext.Value(`{"notes":"secret note content"}`)
		action, taints := hook(ctx, types.ToolSpec{Name: "notes_write", Effect: types.ReadOnly}, args)
		if action != types.TaintDeny {
			t.Fatalf("action = %d, want deny", action)
		}
		if len(taints) == 0 {
			t.Fatal("no argument taints reported")
		}
		found := false
		for _, taint := range taints {
			if taint.Arg == "notes" && taint.Match == note && taint.Origins[0] == (types.Origin{Kind: types.OriginTool, Name: "notes"}) {
				found = true
			}
		}
		if !found {
			t.Fatalf("taints = %+v, want notes arg tainted from OriginTool{notes}", taints)
		}
	})

	t.Run("slot is capped", func(t *testing.T) {
		small, err := New(store, WithMaxNotes(8))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		writeNote(t, small, runInfo("s-cap"), strings.Repeat("x", 64))
		if got := slotText(t, small, runInfo("s-cap")); len(got) != 8 {
			t.Fatalf("capped slot length = %d, want 8", len(got))
		}
	})
}
