package http

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestFetchWithoutEgressClientFailsPermanently(t *testing.T) {
	tool, err := Fetch(types.EgressPolicy{Allow: []string{"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(FetchInput{URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != types.Failed || result.Error == nil || result.Error.Kind != types.Permanent || !strings.Contains(result.Error.Message, "egress client") {
		t.Fatalf("unexpected missing-client result: %#v", result)
	}
}

func TestFetchSpec(t *testing.T) {
	policy := types.EgressPolicy{Allow: []string{"example.com"}}
	tool, err := Fetch(policy)
	if err != nil {
		t.Fatal(err)
	}
	spec := tool.Spec()
	if spec.Name != "web_fetch" || !spec.Network || spec.Effect != types.ReadOnly || spec.Egress == nil || len(spec.Egress.Allow) != 1 || spec.Egress.Allow[0] != "example.com" {
		t.Fatalf("unexpected spec: %#v", spec)
	}
}
