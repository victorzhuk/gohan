package redis

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestEventRecordKindRoundTrip(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	events := []types.Event{
		types.TextDelta{Turn: 1, MessageID: "m1", Delta: "he"},
		types.ToolFinished{
			Turn: 2,
			Result: types.ToolResult{
				ID:      "t1",
				Content: []types.Block{types.Text{Text: "out"}},
				Outcome: types.Succeeded,
			},
			Replayed: true,
		},
		types.Compacted{FromVersion: 3, ToVersion: 7, TokensBefore: 900, TokensAfter: 120, Policy: "auto", PublicationID: "pub-1"},
		types.Done{},
	}
	for _, payload := range events {
		kind, err := eventKind(payload)
		if err != nil {
			t.Fatalf("eventKind(%T): %v", payload, err)
		}
		raw, err := encodeEventRecord(stores.Event{Payload: payload, Meta: types.EventMeta{Seq: 4, Time: now, SessionID: "s1"}})
		if err != nil {
			t.Fatalf("encodeEventRecord(%T): %v", payload, err)
		}
		got, err := decodeEventRecord(context.Background(), stores.NewSchemaRegistry(), raw)
		if err != nil {
			t.Fatalf("decodeEventRecord(%T): %v", payload, err)
		}
		if kind, _ := eventKind(got.Payload); kind == "" {
			t.Fatalf("decoded %T has no kind", got.Payload)
		}
		want, err := encodeEventRecord(got)
		if err != nil {
			t.Fatalf("re-encode %T: %v", payload, err)
		}
		if !bytes.Equal(raw, want) {
			t.Fatalf("%s record changed through a decode and re-encode cycle", kind)
		}
		if got.Meta != (types.EventMeta{Seq: 4, Time: now, SessionID: "s1"}) {
			t.Fatalf("%s meta did not survive the cycle: %+v", kind, got.Meta)
		}
	}
}

func TestToolFinishedWireProjection(t *testing.T) {
	e := stores.Event{
		Payload: types.ToolFinished{
			Turn: 1,
			Result: types.ToolResult{
				ID: "t1",
				Content: []types.Block{
					types.Text{Text: "keep"},
					types.Raw{Provider: "p", Value: func() {}},
				},
			},
		},
	}
	raw, err := encodeEventRecord(e)
	if err != nil {
		t.Fatalf("encodeEventRecord: %v", err)
	}
	got, err := decodeEventRecord(context.Background(), stores.NewSchemaRegistry(), raw)
	if err != nil {
		t.Fatalf("decodeEventRecord: %v", err)
	}
	fin, ok := got.Payload.(types.ToolFinished)
	if !ok {
		t.Fatalf("payload %#v, want types.ToolFinished", got.Payload)
	}
	if len(fin.Result.Content) != 1 {
		t.Fatalf("wire projection kept %d blocks, want the one representable block", len(fin.Result.Content))
	}
}

func TestCompactionSemanticProjection(t *testing.T) {
	e := stores.Event{
		Payload: types.Compacted{FromVersion: 2, PublicationID: "pub"},
		Meta: types.EventMeta{
			SessionID: "s1", RunID: "wrong", RootRunID: "r0", ParentRunID: "r1",
			Depth: 2, Flow: "main", Seq: 9, Time: time.Now(),
		},
	}
	got := compactionEventSemantic(e, "run-7")
	if got.Meta.RunID != "run-7" {
		t.Fatalf("semantic RunID = %q, want the forced partition", got.Meta.RunID)
	}
	if got.Meta.Seq != 0 || !got.Meta.Time.IsZero() {
		t.Fatalf("semantic meta keeps sink-owned fields: %+v", got.Meta)
	}
	want := types.EventMeta{SessionID: "s1", RunID: "run-7", RootRunID: "r0", ParentRunID: "r1", Depth: 2, Flow: "main"}
	if got.Meta != want {
		t.Fatalf("semantic meta = %+v, want %+v", got.Meta, want)
	}
}

func TestCompactionFingerprintDeterminism(t *testing.T) {
	e := stores.Event{
		Payload: types.Compacted{FromVersion: 2, ToVersion: 5, PublicationID: "pub"},
		Meta:    types.EventMeta{SessionID: "s1", RunID: "ignored", Depth: 1, Seq: 3, Time: time.Now()},
	}
	semantic := compactionEventSemantic(e, "run-1")
	a, err := compactionEventFingerprint(semantic)
	if err != nil {
		t.Fatalf("compactionEventFingerprint: %v", err)
	}
	b, err := compactionEventFingerprint(compactionEventSemantic(e, "run-1"))
	if err != nil {
		t.Fatalf("compactionEventFingerprint: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("fingerprint is not deterministic for the same identity: %x != %x", a, b)
	}
	other, err := compactionEventFingerprint(compactionEventSemantic(e, "other"))
	if err != nil {
		t.Fatalf("compactionEventFingerprint: %v", err)
	}
	if bytes.Equal(a, other) {
		t.Fatalf("fingerprint ignored the run partition: %x == %x", a, other)
	}
	changed := semantic
	changed.Payload = types.Compacted{FromVersion: 2, ToVersion: 6, PublicationID: "pub"}
	c, err := compactionEventFingerprint(changed)
	if err != nil {
		t.Fatalf("compactionEventFingerprint: %v", err)
	}
	if bytes.Equal(a, c) {
		t.Fatalf("fingerprint ignored a payload change")
	}
}
