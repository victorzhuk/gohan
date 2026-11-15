package redis

import (
	"strings"
	"testing"
	"time"
)

func TestPoolValidate(t *testing.T) {
	base := PoolConfig{Name: "p", Limit: 10, Window: time.Second}
	cases := []struct {
		name    string
		mutate  func(*PoolConfig)
		want    string
		wantCtx string
	}{
		{"empty name", func(c *PoolConfig) { c.Name = "" }, "name is empty", `pool ""`},
		{"zero limit", func(c *PoolConfig) { c.Limit = 0 }, "limit must be positive", `pool "p"`},
		{"negative limit", func(c *PoolConfig) { c.Limit = -1 }, "limit must be positive", `pool "p"`},
		{"zero window", func(c *PoolConfig) { c.Window = 0 }, "window below 1ms", `pool "p"`},
		{"sub-millisecond window", func(c *PoolConfig) { c.Window = time.Microsecond }, "window below 1ms", `pool "p"`},
		{"negative queue wait", func(c *PoolConfig) { c.QueueWait = -time.Second }, "queue wait is negative", `pool "p"`},
		{"negative anomaly factor", func(c *PoolConfig) { c.AnomalyFactor = -1 }, "anomaly factor is negative", `pool "p"`},
		{"share above one", func(c *PoolConfig) { c.Shares = ClassShares{Interactive: 1.5, Agentic: 0.3, Batch: 0.2} }, "outside", `pool "p"`},
		{"share below zero", func(c *PoolConfig) { c.Shares = ClassShares{Interactive: 0.5, Agentic: -0.1, Batch: 0.6} }, "outside", `pool "p"`},
		{"shares not summing to one", func(c *PoolConfig) { c.Shares = ClassShares{Interactive: 0.5, Agentic: 0.3, Batch: 0.3} }, "sum to 1", `pool "p"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			err := cfg.validate()
			if err == nil {
				t.Fatal("validate = nil, want error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate err = %v, want containing %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.wantCtx) {
				t.Fatalf("validate err = %v, want pool name context %q", err, tc.wantCtx)
			}
		})
	}
	t.Run("valid defaults", func(t *testing.T) {
		if err := base.validate(); err != nil {
			t.Fatalf("validate err = %v, want nil", err)
		}
	})
}

func TestDefaultClassShares(t *testing.T) {
	shares := DefaultClassShares()
	if shares.Interactive != 0.5 || shares.Agentic != 0.3 || shares.Batch != 0.2 {
		t.Fatalf("DefaultClassShares = %+v, want 0.5/0.3/0.2", shares)
	}
}

func TestClassCaps(t *testing.T) {
	cases := []struct {
		name                string
		limit               int64
		shares              ClassShares
		wantI, wantA, wantB int64
	}{
		{"limit 10 default", 10, DefaultClassShares(), 5, 3, 2},
		{"limit 7 default", 7, DefaultClassShares(), 4, 2, 1},
		{"limit 13 fractional remainder", 13, DefaultClassShares(), 8, 3, 2},
		{"limit 10 custom", 10, ClassShares{Interactive: 0.1, Agentic: 0.45, Batch: 0.45}, 2, 4, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i, a, b := classCaps(tc.limit, tc.shares)
			if i != tc.wantI || a != tc.wantA || b != tc.wantB {
				t.Fatalf("classCaps = %d/%d/%d, want %d/%d/%d", i, a, b, tc.wantI, tc.wantA, tc.wantB)
			}
			if i+a+b != tc.limit {
				t.Fatalf("caps %d+%d+%d != limit %d", i, a, b, tc.limit)
			}
		})
	}
}
