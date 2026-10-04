package types

import (
	"errors"
	"strings"
	"testing"
)

func TestToolEgressContract(t *testing.T) {
	t.Run("tools.egress-policy-required", func(t *testing.T) {
		spec := ToolSpec{Name: "web_fetch", Capabilities: Capabilities{Exfil: true}}
		if !RequiresEgress(spec, true) {
			t.Fatal("network tool must require an egress policy")
		}
		err := CheckEgress(spec, true)
		if !errors.Is(err, ErrEgressPolicyRequired) {
			t.Fatalf("got %v, want ErrEgressPolicyRequired", err)
		}
		if !strings.Contains(err.Error(), "web_fetch") {
			t.Fatalf("error %q does not name the tool", err)
		}
		if err := CheckEgress(spec, false); err != nil {
			t.Fatalf("non-network tool without policy: got %v", err)
		}
		policy := EgressPolicy{Allow: []string{"api.example.com"}}
		if err := CheckEgress(ToolSpec{Name: "web_fetch", Egress: &policy}, true); err != nil {
			t.Fatalf("network tool with policy: got %v", err)
		}
	})

	t.Run("build.exfil-derived-from-egress", func(t *testing.T) {
		spec := ToolSpec{
			Name: "internal_fetch",
			Egress: &EgressPolicy{
				Allow:         []string{"*.internal.example"},
				PrivateRanges: AllowPrivate,
			},
		}
		if DeriveExfil(spec) {
			t.Fatal("allow list holding only private entries must derive Exfil false")
		}
		spec.Egress.Allow = append(spec.Egress.Allow, "api.example.com")
		if !DeriveExfil(spec) {
			t.Fatal("adding a public host must derive Exfil true")
		}
	})
}
