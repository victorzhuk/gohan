package stores

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMemoryRawRecords(t *testing.T) {
	raw := NewMemoryRawRecords()
	env := RecordEnvelope{Shape: "Message", SchemaVersion: 1, Payload: json.RawMessage(`{"role":"user"}`)}
	if err := raw.PutRaw(context.Background(), "session/message", env); err != nil {
		t.Fatal(err)
	}
	got, err := raw.GetRaw(context.Background(), "session/message")
	if err != nil {
		t.Fatal(err)
	}
	if got.Shape != env.Shape || got.SchemaVersion != env.SchemaVersion || string(got.Payload) != string(env.Payload) {
		t.Fatalf("GetRaw = %+v, want %+v", got, env)
	}
}
