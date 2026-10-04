package storetest

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestSchemaFixtures(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range schemaFixtures {
		id := fmt.Sprintf("%s@%d", f.Shape, f.Version)
		if seen[id] {
			t.Fatalf("duplicate fixture %s", id)
		}
		seen[id] = true
		if f.Shape == "" {
			t.Fatalf("fixture at version %d has an empty shape", f.Version)
		}
		if f.Version < 1 {
			t.Fatalf("fixture %s has version %d below 1", f.Shape, f.Version)
		}
		if !json.Valid([]byte(f.Data)) {
			t.Fatalf("fixture %s is not valid JSON: %q", id, f.Data)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(f.Data), &doc); err != nil {
			t.Fatalf("fixture %s is not a JSON object: %v", id, err)
		}
	}
}
