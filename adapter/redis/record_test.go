package redis

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
)

type sampleEvent struct {
	RunID   string    `json:"run_id"`
	Kind    string    `json:"kind"`
	At      time.Time `json:"at"`
	Payload []byte    `json:"payload,omitempty"`
}

func TestEncodeDecodeRecordRoundTrip(t *testing.T) {
	reg := stores.NewSchemaRegistry()
	at := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	in := sampleEvent{RunID: "r1", Kind: "step", At: at, Payload: []byte("x")}
	raw, err := encodeRecord("Event", in)
	if err != nil {
		t.Fatalf("encodeRecord: %v", err)
	}
	out, err := decodeRecord[sampleEvent](context.Background(), reg, raw)
	if err != nil {
		t.Fatalf("decodeRecord: %v", err)
	}
	if out.RunID != in.RunID || out.Kind != in.Kind || !out.At.Equal(at) || string(out.Payload) != "x" {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

func TestDecodeRecordForwardCompatible(t *testing.T) {
	reg := stores.NewSchemaRegistry()
	payload, err := json.Marshal(map[string]any{
		"run_id":       "r2",
		"kind":         "step",
		"at":           time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC),
		"future_field": map[string]string{"later": "value"},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	raw, err := encodeRecord("Event", json.RawMessage(payload))
	if err != nil {
		t.Fatalf("encodeRecord: %v", err)
	}
	out, err := decodeRecord[sampleEvent](context.Background(), reg, raw)
	if err != nil {
		t.Fatalf("decodeRecord: %v", err)
	}
	if out.RunID != "r2" || out.Kind != "step" {
		t.Fatalf("decode mismatch: %+v", out)
	}
}

func TestKeyRootDefaults(t *testing.T) {
	c := defaultConfig()
	if got, want := c.root(), "gohan:"; got != want {
		t.Fatalf("root() = %q, want %q", got, want)
	}
	if got, want := c.key("pool", "main"), "gohan:pool:main"; got != want {
		t.Fatalf("key() = %q, want %q", got, want)
	}
}

func TestKeyRootTenantScoped(t *testing.T) {
	c := defaultConfig()
	WithTenant("acme")(c)
	if got, want := c.root(), "gohan:acme:"; got != want {
		t.Fatalf("root() = %q, want %q", got, want)
	}
	if got, want := c.key("pool", "main"), "gohan:acme:pool:main"; got != want {
		t.Fatalf("key() = %q, want %q", got, want)
	}
}
