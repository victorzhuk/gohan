package types

import (
	"testing"
	"time"
)

func TestModelProfile(t *testing.T) {
	t.Run("profile-fields", func(t *testing.T) {
		p := ModelProfile{
			Name:          "claude-sonnet",
			Version:       "4-20250514",
			Sunset:        time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
			Successor:     "claude-opus",
			Region:        "eu-central",
			Endpoint:      "https://api.example.com",
			QuotaPool:     "org-main",
			ContextWindow: 200000,
			MaxInFlight:   8,
			Affinity:      AffinitySessionHash,
			LatencyClass:  Interactive,
			Timeout:       ModelTimeout{Connect: 5 * time.Second, FirstChunk: 10 * time.Second, Idle: 15 * time.Second},
			Keys:          TenantKey,
		}
		if p.Name == "" || p.Version == "" || p.QuotaPool == "" {
			t.Fatal("profile identity fields must round-trip")
		}
		if p.Timeout.Connect != 5*time.Second || p.Timeout.Idle != 15*time.Second {
			t.Fatalf("timeout = %+v, want connect 5s idle 15s", p.Timeout)
		}
		if p.Keys != TenantKey || p.LatencyClass != Interactive || p.Affinity != AffinitySessionHash {
			t.Fatalf("enum fields = %d/%d/%d, want tenant key, interactive, session hash", p.Keys, p.LatencyClass, p.Affinity)
		}
	})

	t.Run("caps-shape", func(t *testing.T) {
		caps := Caps{
			Tools:            true,
			ParallelTools:    true,
			Constrained:      true,
			Streaming:        true,
			Cache:            CacheExplicit,
			CacheBreakpoints: 4,
			Compaction:       CompactionOpaqueCap,
			Fidelity:         map[BlockKind]Fidelity{KindFile: Degraded},
			ProviderTools:    map[string]ProviderToolCap{"web_search": {Approval: true}},
			Blobs:            BlobCaps{MaxBytes: 5 << 20, MaxPerRequest: 20, Formats: []string{"image/png"}},
			StreamsToolArgs:  true,
		}
		if caps.Cache != CacheExplicit || caps.Compaction != CompactionOpaqueCap {
			t.Fatalf("modes = %d/%d, want explicit cache, opaque compaction", caps.Cache, caps.Compaction)
		}
		if caps.Fidelity[KindFile] != Degraded {
			t.Fatalf("fidelity[file] = %d, want degraded", caps.Fidelity[KindFile])
		}
		if !caps.ProviderTools["web_search"].Approval {
			t.Fatal("provider tool approval not carried")
		}
		if caps.Blobs.MaxBytes != 5<<20 || len(caps.Blobs.Formats) != 1 {
			t.Fatalf("blob caps = %+v, want 5 MiB and one format", caps.Blobs)
		}
	})

	t.Run("pricing-shape", func(t *testing.T) {
		p := Pricing{
			Input:                3.0,
			CachedInput:          0.3,
			CacheWrite:           3.75,
			Output:               15.0,
			BatchDiscount:        0.5,
			SocializeCacheWrites: true,
			SandboxSecond:        0.01,
			ProviderCall:         map[string]float64{"web_search": 0.01},
		}
		if !p.SocializeCacheWrites || p.ProviderCall["web_search"] != 0.01 {
			t.Fatalf("pricing = %+v, want socialized writes and provider call price", p)
		}
	})

	t.Run("keymode-defaults-to-platform", func(t *testing.T) {
		if PlatformKey != 0 {
			t.Fatalf("PlatformKey = %d, want the zero value", PlatformKey)
		}
		modes := map[KeyMode]bool{PlatformKey: true, TenantKey: true, TenantOrPlatformKey: true}
		if len(modes) != 3 {
			t.Fatal("KeyMode constants are not distinct")
		}
	})

	t.Run("class-normalisation", func(t *testing.T) {
		cases := []struct {
			name   string
			status int
			code   string
			want   ErrorClass
		}{
			{"429", 429, "", ClassRateLimited},
			{"quota code", 400, "insufficient_quota", ClassRateLimited},
			{"quota code case", 400, "Insufficient_Quota", ClassRateLimited},
			{"5xx", 503, "", ClassTransient},
			{"context length", 400, "context_length_exceeded", ClassContextOverflow},
			{"content policy", 400, "content_policy_violation", ClassContentPolicy},
			{"retired model", 404, "model_not_found", ClassDeprecated},
			{"401", 401, "", ClassAuth},
			{"403", 403, "", ClassAuth},
			{"400", 400, "invalid_parameter", ClassPermanent},
			{"404 unrecognised", 404, "", ClassPermanent},
		}
		for _, tc := range cases {
			if got := ClassifyProviderError(tc.status, tc.code); got != tc.want {
				t.Errorf("%s: ClassifyProviderError(%d, %q) = %d, want %d", tc.name, tc.status, tc.code, got, tc.want)
			}
		}
	})
}
