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
	want := stores.Fingerprint(`exact-v1:book:{"city":"Oslo","date":{"day":2,"month":7}}`)
	if got != want {
		t.Fatalf("fingerprint = %q, want canonical fingerprint %q", got, want)
	}

	n92 := ToolFingerprint(spec, json.RawMessage(`{"city":"Oslo","n":9007199254740992}`))
	if n92 == ToolFingerprint(spec, json.RawMessage(`{"city":"Oslo","n":9007199254740993}`)) {
		t.Fatal("adjacent integers collapsed into one fingerprint")
	}
	if ToolFingerprint(spec, json.RawMessage(`{"city":"Oslo","n":1e2}`)) != ToolFingerprint(spec, json.RawMessage(`{"city":"Oslo","n":100.0}`)) {
		t.Fatal("decimal-equivalent arguments produced different fingerprints")
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
