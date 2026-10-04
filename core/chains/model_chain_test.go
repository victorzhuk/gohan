package chains

import "testing"

func TestModelChain(t *testing.T) {
	if err := ValidateModelChain(nil); err != nil {
		t.Fatalf("empty chain: %v", err)
	}

	valid := ModelChain{
		{Name: "router", Kind: KindRouter},
		{Name: "hedge", Kind: KindHedge},
		{Name: "fallback", Kind: KindFallback},
		{Name: "budget", Kind: KindBudget},
		{Name: "hooks", Kind: KindHooks},
	}
	if err := ValidateModelChain(valid); err != nil {
		t.Fatalf("canonical order rejected: %v", err)
	}

	for _, tc := range []struct {
		name string
		ch   ModelChain
	}{
		{"fallback outside hedge", ModelChain{
			{Name: "fallback", Kind: KindFallback},
			{Name: "hedge", Kind: KindHedge},
		}},
		{"hedge outside router", ModelChain{
			{Name: "hedge", Kind: KindHedge},
			{Name: "router", Kind: KindRouter},
		}},
		{"budget inside hooks", ModelChain{
			{Name: "hooks", Kind: KindHooks},
			{Name: "budget", Kind: KindBudget},
		}},
	} {
		if err := ValidateModelChain(tc.ch); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}
