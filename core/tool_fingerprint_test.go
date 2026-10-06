package gohan

import (
	"encoding/json"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestToolFingerprintCanonicalizesAllArguments(t *testing.T) {
	spec := types.ToolSpec{Name: "book"}
	first := json.RawMessage(`{"city":"Oslo","date":{"month":7,"day":2}}`)
	second := json.RawMessage("{ \"date\" : {\"day\":2,\"month\":7}, \"city\":\"Oslo\" }")

	got, reordered := ToolFingerprint(spec, first), ToolFingerprint(spec, second)
	if got != reordered {
		t.Fatalf("fingerprints differ for equivalent arguments: %q != %q", got, reordered)
	}
	want := stores.Fingerprint(`book:{"city":"Oslo","date":{"day":2,"month":7}}`)
	if got != want {
		t.Fatalf("fingerprint = %q, want canonical fingerprint %q", got, want)
	}
}

func TestToolFingerprintCanonicalizesSelectedArguments(t *testing.T) {
	spec := types.ToolSpec{Name: "book", FingerprintFields: []string{"date"}}
	first := json.RawMessage(`{"date":{"month":7,"day":2},"note":"one"}`)
	second := json.RawMessage(`{"note":"two","date":{"day":2,"month":7}}`)

	if got, reordered := ToolFingerprint(spec, first), ToolFingerprint(spec, second); got != reordered {
		t.Fatalf("fingerprints differ for equivalent selected arguments: %q != %q", got, reordered)
	}
}
