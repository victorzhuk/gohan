//go:build full

package redis_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/victorzhuk/gohan/adapter/redis"
	"github.com/victorzhuk/gohan/core/types"
)

func newPool(t *testing.T, client goredis.UniversalClient, prefix string, cfg redis.PoolConfig, opts ...redis.Option) *redis.Pool {
	t.Helper()
	base := []redis.Option{redis.WithPrefix(prefix)}
	opts = append(base, opts...)
	pool, err := redis.NewPool(client, cfg, opts...)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	return pool
}

func admitClass(t *testing.T, pool *redis.Pool, class types.LatencyClass, waited time.Duration) (redis.Decision, error) {
	t.Helper()
	return pool.Admit(context.Background(), redis.Admission{Class: class, Waited: waited})
}

// shiftedClient overrides TIME so the pool's store clock runs shift behind
// the real Redis server clock.
type shiftedClient struct {
	*goredis.Client
	shift time.Duration
}

func (c *shiftedClient) Time(ctx context.Context) *goredis.TimeCmd {
	cmd := goredis.NewTimeCmd(ctx)
	cmd.SetVal(time.Now().Add(-c.shift))
	return cmd
}

func TestRedisPoolScenarios(t *testing.T) {
	ctx := context.Background()
	var prefixSeq int
	nextPrefix := func() string {
		prefixSeq++
		return fmt.Sprintf("gohanpool%d", prefixSeq)
	}

	t.Run("stores.shared-pool-across-services", func(t *testing.T) {
		prefix := nextPrefix()
		cfg := redis.PoolConfig{Name: "quota", Limit: 3, Window: 5 * time.Second}
		clientA := newRedisClient(t)
		clientB := goredis.NewClient(&goredis.Options{Addr: clientA.Options().Addr})
		t.Cleanup(func() { _ = clientB.Close() })
		poolA := newPool(t, clientA, prefix, cfg)
		poolB := newPool(t, clientB, prefix, cfg)
		admitted := 0
		for _, pool := range []*redis.Pool{poolA, poolB} {
			for range 3 {
				_, err := admitClass(t, pool, types.Interactive, 0)
				if err == nil {
					admitted++
					continue
				}
				if !errors.Is(err, redis.ErrPoolExhausted) {
					t.Fatalf("admission err = %v, want ErrPoolExhausted", err)
				}
			}
		}
		if admitted != 3 {
			t.Fatalf("total admitted = %d, want 3", admitted)
		}
		bucket := clientA.HGetAll(ctx, prefix+":pool:"+cfg.Name+":bucket:interactive").Val()
		if len(bucket) == 0 {
			t.Fatal("interactive bucket hash missing")
		}
		tokens, err := strconv.ParseFloat(bucket["tokens"], 64)
		if err != nil {
			t.Fatalf("decode tokens: %v", err)
		}
		if tokens >= 3 {
			t.Fatalf("bucket tokens = %g, want below cap 3", tokens)
		}
		poolC := newPool(t, clientA, prefix, cfg)
		if _, err := admitClass(t, poolC, types.Interactive, 0); !errors.Is(err, redis.ErrPoolExhausted) {
			t.Fatalf("fresh pool admission err = %v, want ErrPoolExhausted", err)
		}
	})

	t.Run("stores.batch-yields-to-interactive", func(t *testing.T) {
		prefix := nextPrefix()
		now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
		clock := &now
		cfg := redis.PoolConfig{Name: "quota", Limit: 100, Window: 30 * time.Second, QueueWait: 100 * time.Millisecond}
		client := newRedisClient(t)
		deferred := 0
		var deferredClass types.LatencyClass
		pool := newPool(t, client, prefix, cfg, redis.WithClock(func() time.Time { return *clock }), redis.WithTelemetry(redis.Telemetry{
			Deferred: func(class types.LatencyClass) {
				deferred++
				deferredClass = class
			},
		}))
		if _, err := admitClass(t, pool, types.Interactive, 500*time.Millisecond); err != nil {
			t.Fatalf("interactive admission err = %v", err)
		}
		dec, err := admitClass(t, pool, types.Batch, 0)
		if !errors.Is(err, redis.ErrDeferred) {
			t.Fatalf("batch admission err = %v, want ErrDeferred", err)
		}
		if dec.RetryAfter <= 0 || dec.RetryAfter > cfg.Window {
			t.Fatalf("deferral RetryAfter = %v, want in (0, window]", dec.RetryAfter)
		}
		if deferred != 1 || deferredClass != types.Batch {
			t.Fatalf("deferred telemetry = %d (%v), want 1 (batch)", deferred, deferredClass)
		}
		*clock = clock.Add(31 * time.Second)
		if _, err := admitClass(t, pool, types.Batch, 0); err != nil {
			t.Fatalf("batch admission after window err = %v", err)
		}
	})

	t.Run("stores.daily-pool-budget", func(t *testing.T) {
		prefix := nextPrefix()
		cfg := redis.PoolConfig{Name: "quota", Limit: 100, Window: time.Minute, DailyLimit: 10}
		client := newRedisClient(t)
		pool := newPool(t, client, prefix, cfg)
		if err := pool.Spend(ctx, 4); err != nil {
			t.Fatalf("spend 4 err = %v", err)
		}
		if err := pool.Spend(ctx, 6); err != nil {
			t.Fatalf("spend 6 err = %v", err)
		}
		err := pool.Spend(ctx, 1)
		var limit *types.LimitExceededError
		if !errors.As(err, &limit) || limit.Limit != "pool/day" {
			t.Fatalf("spend 1 err = %v, want pool/day limit error", err)
		}
		_, err = admitClass(t, pool, types.Interactive, 0)
		if !errors.As(err, &limit) || limit.Limit != "pool/day" {
			t.Fatalf("admit err = %v, want pool/day limit error", err)
		}
		if kinds := types.ProblemOf(err).Kind; kinds != types.Permanent {
			t.Fatalf("problem kind = %v, want permanent", kinds)
		}
	})

	t.Run("stores.redis-limiter-store-clock", func(t *testing.T) {
		prefix := nextPrefix()
		cfg := redis.PoolConfig{Name: "quota", Limit: 2, Window: 2 * time.Second}
		client := newRedisClient(t)
		pool := newPool(t, client, prefix, cfg)
		for range 2 {
			if _, err := admitClass(t, pool, types.Interactive, 0); err != nil {
				t.Fatalf("admission err = %v", err)
			}
		}
		dec, err := admitClass(t, pool, types.Interactive, 0)
		if !errors.Is(err, redis.ErrPoolExhausted) {
			t.Fatalf("third admission err = %v, want ErrPoolExhausted", err)
		}
		if dec.RetryAfter <= 0 || dec.RetryAfter > 2*time.Second {
			t.Fatalf("RetryAfter = %v, want in (0, 2s]", dec.RetryAfter)
		}
		// The refusal reports the refill horizon for one token, not the
		// window remainder: Window/Limit = 1s here, and a caller clock
		// cannot shorten it.
		refill := cfg.Window / time.Duration(cfg.Limit)
		if dec.RetryAfter < refill-200*time.Millisecond || dec.RetryAfter > refill+200*time.Millisecond {
			t.Fatalf("RetryAfter = %v, want within 200ms of refill %v", dec.RetryAfter, refill)
		}
		time.Sleep(dec.RetryAfter + 50*time.Millisecond)
		if _, err := admitClass(t, pool, types.Interactive, 0); err != nil {
			t.Fatalf("admission after retry window err = %v", err)
		}
	})

	t.Run("stores.redis-admission-class-share", func(t *testing.T) {
		prefix := nextPrefix()
		cfg := redis.PoolConfig{Name: "quota", Limit: 10, Window: 30 * time.Second}
		client := newRedisClient(t)
		pool := newPool(t, client, prefix, cfg)
		for range 2 {
			if _, err := admitClass(t, pool, types.Batch, 0); err != nil {
				t.Fatalf("batch admission err = %v", err)
			}
		}
		for range 3 {
			if _, err := admitClass(t, pool, types.Agentic, 0); err != nil {
				t.Fatalf("agentic admission err = %v", err)
			}
		}
		for range 5 {
			if _, err := admitClass(t, pool, types.Interactive, 0); err != nil {
				t.Fatalf("interactive admission err = %v", err)
			}
		}
		if _, err := admitClass(t, pool, types.Interactive, 0); !errors.Is(err, redis.ErrPoolExhausted) {
			t.Fatalf("interactive refusal err = %v, want ErrPoolExhausted", err)
		}
		if _, err := admitClass(t, pool, types.Agentic, 0); !errors.Is(err, redis.ErrPoolExhausted) {
			t.Fatalf("agentic refusal err = %v, want ErrPoolExhausted", err)
		}
		if _, err := admitClass(t, pool, types.Batch, 0); !errors.Is(err, redis.ErrPoolExhausted) {
			t.Fatalf("batch refusal err = %v, want ErrPoolExhausted", err)
		}
	})

	t.Run("stores.redis-pool-budget-window-reset", func(t *testing.T) {
		prefix := nextPrefix()
		now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
		clock := &now
		cfg := redis.PoolConfig{Name: "quota", Limit: 10, Window: time.Hour, DailyLimit: 10}
		client := newRedisClient(t)
		pool := newPool(t, client, prefix, cfg, redis.WithClock(func() time.Time { return *clock }))
		if err := pool.Spend(ctx, 10); err != nil {
			t.Fatalf("spend 10 err = %v", err)
		}
		_, err := admitClass(t, pool, types.Interactive, 0)
		var limit *types.LimitExceededError
		if !errors.As(err, &limit) || limit.Limit != "pool/day" {
			t.Fatalf("admit err = %v, want pool/day limit error", err)
		}
		*clock = clock.Add(24 * time.Hour)
		if _, err := admitClass(t, pool, types.Interactive, 0); err != nil {
			t.Fatalf("admit after day reset err = %v", err)
		}
		spend, err := pool.DailySpend(ctx)
		if err != nil {
			t.Fatalf("daily spend err = %v", err)
		}
		if spend != 0 {
			t.Fatalf("daily spend = %g, want 0", spend)
		}
	})

	t.Run("cost anomaly compares with the previous day", func(t *testing.T) {
		prefix := nextPrefix()
		now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
		clock := &now
		cfg := redis.PoolConfig{Name: "quota", Limit: 100, Window: time.Hour, AnomalyFactor: 3}
		client := newRedisClient(t)
		anomalies := 0
		pool := newPool(t, client, prefix, cfg, redis.WithClock(func() time.Time { return *clock }), redis.WithTelemetry(redis.Telemetry{
			CostAnomaly: func() { anomalies++ },
		}))
		*clock = clock.AddDate(0, 0, -2)
		if err := pool.Spend(ctx, 5); err != nil {
			t.Fatalf("spend day-2 err = %v", err)
		}
		*clock = clock.AddDate(0, 0, 1)
		if err := pool.Spend(ctx, 15); err != nil {
			t.Fatalf("spend day-1 err = %v", err)
		}
		*clock = clock.AddDate(0, 0, 1)
		if err := pool.Spend(ctx, 29); err != nil {
			t.Fatalf("spend 29 err = %v", err)
		}
		if anomalies != 0 {
			t.Fatalf("anomalies after 29 vs mean 10 = %d, want 0", anomalies)
		}
		if err := pool.Spend(ctx, 2); err != nil {
			t.Fatalf("spend 2 err = %v", err)
		}
		if anomalies != 1 {
			t.Fatalf("anomalies after total 31 = %d, want 1", anomalies)
		}
	})

	t.Run("budget clock follows the client time command", func(t *testing.T) {
		prefix := nextPrefix()
		cfg := redis.PoolConfig{Name: "quota", Limit: 10, Window: time.Hour}
		client := newRedisClient(t)
		pool := newPool(t, client, prefix, cfg)
		shifted := &shiftedClient{Client: client, shift: 48 * time.Hour}
		shiftedPool := newPool(t, shifted, prefix, cfg)
		if err := shiftedPool.Spend(ctx, 1); err != nil {
			t.Fatalf("shifted spend err = %v", err)
		}
		spend, err := pool.DailySpend(ctx)
		if err != nil {
			t.Fatalf("daily spend err = %v", err)
		}
		if spend != 0 {
			t.Fatalf("daily spend on the real day = %g, want 0", spend)
		}
		day := time.Now().UTC().Add(-48 * time.Hour).Format("2006-01-02")
		value := client.Get(ctx, prefix+":pool:"+cfg.Name+":spend:"+day).Val()
		if value != "1" {
			t.Fatalf("shifted day key = %q, want 1", value)
		}
	})

	t.Run("two clocks agree on the window", func(t *testing.T) {
		prefix := nextPrefix()
		cfg := redis.PoolConfig{Name: "quota", Limit: 1, Window: time.Second}
		client := newRedisClient(t)
		pool := newPool(t, client, prefix, cfg)
		if _, err := admitClass(t, pool, types.Interactive, 0); err != nil {
			t.Fatalf("first admission err = %v", err)
		}
		dec, err := admitClass(t, pool, types.Interactive, 0)
		if !errors.Is(err, redis.ErrPoolExhausted) {
			t.Fatalf("second admission err = %v, want ErrPoolExhausted", err)
		}
		if dec.RetryAfter <= 0 || dec.RetryAfter > cfg.Window {
			t.Fatalf("RetryAfter = %v, want in (0, window]", dec.RetryAfter)
		}
		refill := cfg.Window / time.Duration(cfg.Limit)
		if dec.RetryAfter < refill-200*time.Millisecond || dec.RetryAfter > refill+200*time.Millisecond {
			t.Fatalf("RetryAfter = %v, want within 200ms of refill %v", dec.RetryAfter, refill)
		}
		time.Sleep(dec.RetryAfter + 50*time.Millisecond)
		if _, err := admitClass(t, pool, types.Interactive, 0); err != nil {
			t.Fatalf("admission after retry window err = %v", err)
		}
	})
}
