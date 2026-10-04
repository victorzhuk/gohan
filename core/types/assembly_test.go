package types

import (
	"context"
	"testing"
)

func TestContextSlots(t *testing.T) {
	if SlotStatic != 0 || SlotSession != 1 || SlotTurn != 2 {
		t.Fatalf("slot order changed: %d %d %d", SlotStatic, SlotSession, SlotTurn)
	}
	var p ContextProvider = staticProvider{}
	if p.Slot() != SlotStatic {
		t.Fatalf("Slot() = %d, want %d", p.Slot(), SlotStatic)
	}
	blocks, err := p.Provide(context.Background(), RunInfo{})
	if err != nil || len(blocks) != 1 {
		t.Fatalf("Provide() = %v, %v", blocks, err)
	}
}

type staticProvider struct{}

func (staticProvider) Slot() ContextSlot { return SlotStatic }

func (staticProvider) Provide(context.Context, RunInfo) ([]Block, error) {
	return []Block{Text{Text: "static"}}, nil
}
