package types

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestModelRequest(t *testing.T) {
	temp := 0.7
	in := ModelRequest{
		System: []Block{
			Text{BlockBase: BlockBase{Origin: Origin{Kind: OriginUser, Name: "app"}, Seq: 1}, Text: "be terse"},
		},
		Tools: []ToolSpec{
			{
				Name:        "lookup",
				Description: "find a record",
				Schema:      json.RawMessage(`{"type":"object"}`),
				MaxOutput:   4096,
			},
		},
		Messages: []Message{
			{ID: "m1", Role: RoleUser, Blocks: []Block{Text{Text: "hello"}}},
		},
		Options: ModelOptions{
			MaxTokens:      512,
			Temperature:    &temp,
			ToolChoice:     ToolChoiceAuto,
			ResponseSchema: json.RawMessage(`{"type":"object"}`),
			Priority:       3,
			AffinityKey:    "tenant-a",
			Extra:          map[string]any{"extra_body": map[string]any{"seed": 7.0}},
		},
	}

	b, err := json.Marshal(in) //nolint:staticcheck // MarshalJSONTo strips Verify, which has no wire form
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out ModelRequest
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip changed the request:\nwant %+v\ngot  %+v", in, out)
	}

	again, err := json.Marshal(out) //nolint:staticcheck // MarshalJSONTo strips Verify, which has no wire form
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	if !bytes.Equal(b, again) {
		t.Fatalf("marshaling is not stable")
	}

	// The record/replay key hashes these bytes, so the declared field order
	// is the wire order; a reordered field would silently break replay.
	order := []string{`"System"`, `"Tools"`, `"Messages"`, `"Options"`}
	last := -1
	for _, field := range order {
		at := bytes.Index(b[last+1:], []byte(field))
		if at < 0 {
			t.Fatalf("field %s missing from wire form", field)
		}
		last += at + 1
	}
}
