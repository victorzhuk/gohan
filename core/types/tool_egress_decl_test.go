package types

import "testing"

func TestEgressDenied(t *testing.T) {
	err := EgressDenied{URL: "https://example.com/path", Reason: "private range"}
	want := "gohan: egress to https://example.com/path refused: private range"
	if got := err.Error(); got != want {
		t.Fatalf("EgressDenied.Error() = %q, want %q", got, want)
	}
}
