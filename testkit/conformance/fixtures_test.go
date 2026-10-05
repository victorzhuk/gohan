package conformance

import "testing"

// TestDefaultFixturesCoversScenarioSet pins the fixture set to the scenario
// table: the six status-to-class mappings with Retry-After on the 429, the
// usage contract, the mid-tool-call truncation and the Raw round trip.
func TestDefaultFixturesCoversScenarioSet(t *testing.T) {
	fs := DefaultFixtures()
	want := map[string]bool{
		"rate limited with retry after": false,
		"quota code":                    false,
		"transient":                     false,
		"auth":                          false,
		"permanent":                     false,
		"context overflow":              false,
		"deprecated":                    false,
		"usage reported":                false,
		"usage estimated":               false,
		"truncated tool call":           false,
		"raw round trip":                false,
	}
	for _, fx := range fs {
		if _, ok := want[fx.Name]; !ok {
			t.Errorf("fixture %q is not in the scenario set", fx.Name)
			continue
		}
		if want[fx.Name] {
			t.Errorf("fixture %q appears twice", fx.Name)
		}
		want[fx.Name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("fixture %q missing from DefaultFixtures", name)
		}
	}

	byName := make(map[string]Fixture, len(fs))
	for _, fx := range fs {
		byName[fx.Name] = fx
	}
	if fx := byName["rate limited with retry after"]; fx.RetryAfter != 7e9 {
		t.Errorf("RetryAfter %s, want 7s", fx.RetryAfter)
	}
	if fx := byName["quota code"]; fx.Status != 400 || fx.Code != "insufficient_quota" {
		t.Errorf("quota fixture is %d/%q, want 400/insufficient_quota", fx.Status, fx.Code)
	}
}
