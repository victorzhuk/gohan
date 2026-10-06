package stores

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestCheckpointUpcastRead(t *testing.T) {
	reg := NewSchemaRegistryFor(3)
	for version, suffix := range map[SchemaVersion]string{1: "-v2", 2: "-v3"} {
		if err := reg.Register("Checkpoint", version, func(raw []byte) ([]byte, error) {
			var cp Checkpoint
			if err := json.Unmarshal(raw, &cp); err != nil {
				return nil, err
			}
			cp.Flow += suffix
			return json.Marshal(cp)
		}); err != nil {
			t.Fatal(err)
		}
	}
	s := NewMemoryCheckpoints(WithMemoryCheckpointSchemas(reg))
	var source Checkpoint
	if err := json.Unmarshal([]byte(`{"flow":"booking","schema_version":1}`), &source); err != nil {
		t.Fatal(err)
	}
	token, err := s.Put(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	rec := s.toks[token]
	rec.cp.SchemaVersion = 1
	payload, _ := json.Marshal(source)
	rec.raw, _ = json.Marshal(RecordEnvelope{Shape: "Checkpoint", SchemaVersion: 1, Payload: payload})
	before := append([]byte(nil), rec.raw...)
	s.mu.Unlock()

	got, err := s.Peek(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if got.Flow != "booking-v2-v3" {
		t.Fatalf("Flow = %q", got.Flow)
	}
	s.mu.Lock()
	after := append([]byte(nil), s.toks[token].raw...)
	s.mu.Unlock()
	if string(before) != string(after) {
		t.Fatalf("stored bytes changed: before %s after %s", before, after)
	}
	consumed, err := s.ConsumeIf(context.Background(), token, got, ResumeInput{})
	if err != nil {
		t.Fatalf("ConsumeIf after upcasted Peek: %v", err)
	}
	if consumed.Flow != got.Flow || consumed.SchemaVersion != got.SchemaVersion {
		t.Fatalf("ConsumeIf = %+v, want checkpoint returned by Peek %+v", consumed, got)
	}
	s.mu.Lock()
	afterConsume := append([]byte(nil), s.toks[token].raw...)
	s.mu.Unlock()
	if string(before) != string(afterConsume) {
		t.Fatalf("ConsumeIf changed stored bytes: before %s after %s", before, afterConsume)
	}
}

func TestCheckpointIncompleteSchemaRead(t *testing.T) {
	reg := NewSchemaRegistryFor(3)
	if err := reg.Register("Checkpoint", 1, func(raw []byte) ([]byte, error) { return raw, nil }); err != nil {
		t.Fatal(err)
	}
	s := NewMemoryCheckpoints(WithMemoryCheckpointSchemas(reg))
	token, err := s.Put(context.Background(), Checkpoint{Flow: "stale"})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.toks[token].raw, _ = json.Marshal(RecordEnvelope{Shape: "Checkpoint", SchemaVersion: 1, Payload: []byte(`{"flow":"stale"}`)})
	s.mu.Unlock()
	got, err := s.Peek(context.Background(), token)
	if !errors.Is(err, types.ErrSchemaTooOld) || !checkpointEqual(got, Checkpoint{}) {
		t.Fatalf("Peek = %+v, %v; want zero checkpoint and ErrSchemaTooOld", got, err)
	}
}
