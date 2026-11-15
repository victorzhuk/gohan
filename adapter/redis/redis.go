// Package redis implements the gohan cross-process store slice on Redis:
// the shared quota pool limiter with class admission and the pool/day
// breaker, and one EventLog on Redis streams. It implements no other store
// port. The EventLog's per-run sequence counter and compaction tombstone
// hash never expire by contract: dedupe and numbering must survive
// eviction and expiry, as the Postgres event_seq and
// event_compaction_publication tables do. Deployments must therefore not
// run an evicting maxmemory-policy for these keys; noeviction is the
// supported configuration.
package redis

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const defaultPrefix = "gohan"

// Telemetry carries the meters the module increments. Nil hooks are ignored.
type Telemetry struct {
	Deferred    func(class types.LatencyClass)
	QueueWait   func(wait time.Duration)
	CostAnomaly func()
}

// Option configures the Redis stores.
type Option func(*config)

// WithPrefix sets the key namespace root; the default is "gohan".
func WithPrefix(prefix string) Option {
	return func(c *config) { c.prefix = prefix }
}

// WithTenant scopes every key below one tenant; unset means no tenant.
func WithTenant(tenant string) Option {
	return func(c *config) { c.tenant = tenant }
}

// WithTelemetry registers the meters the module increments.
func WithTelemetry(t Telemetry) Option {
	return func(c *config) { c.telemetry = t }
}

// WithClock replaces the store clock for tests. Production reads the Redis
// server TIME, so a caller's own wall clock never decides a window, an
// expiry or a stamp.
func WithClock(now func() time.Time) Option {
	return func(c *config) { c.now = now }
}

// WithEventLogCapacity sets the retained events per run; the default is
// 1024. Zero keeps everything; negative clamps to zero.
func WithEventLogCapacity(n int) Option {
	return func(c *config) {
		if n < 0 {
			n = 0
		}
		c.eventLogCapacity = n
	}
}

// WithEventLogSchemas sets the schema registry the EventLog records carry.
func WithEventLogSchemas(reg *stores.SchemaRegistry) Option {
	return func(c *config) { c.eventLogSchemas = reg }
}

type config struct {
	prefix           string
	tenant           string
	telemetry        Telemetry
	now              func() time.Time
	eventLogCapacity int
	eventLogSchemas  *stores.SchemaRegistry
}

func defaultConfig() *config {
	return &config{
		prefix:           defaultPrefix,
		eventLogCapacity: 1024,
	}
}

// root returns the key namespace root: "gohan:" or "gohan:<tenant>:".
func (c *config) root() string {
	if c.tenant == "" {
		return c.prefix + ":"
	}
	return c.prefix + ":" + c.tenant + ":"
}

// key joins root and parts with ":".
func (c *config) key(parts ...string) string {
	key := c.root()
	for i, part := range parts {
		if i > 0 {
			key += ":"
		}
		key += part
	}
	return key
}

// nowMS returns the store clock in Unix milliseconds: c.now when
// WithClock set it, otherwise Redis server TIME.
func (c *config) nowMS(ctx context.Context, client goredis.Cmdable) (int64, error) {
	if c.now != nil {
		return c.now().UnixMilli(), nil
	}
	return serverTimeMS(ctx, client)
}

func serverTimeMS(ctx context.Context, client goredis.Cmdable) (int64, error) {
	t, err := client.Time(ctx).Result()
	if err != nil {
		return 0, fmt.Errorf("redis time: %w", err)
	}
	return t.UnixMilli(), nil
}

// ServerNow returns the Redis server TIME as the store clock.
func ServerNow(ctx context.Context, client goredis.Cmdable) (time.Time, error) {
	t, err := client.Time(ctx).Result()
	if err != nil {
		return time.Time{}, fmt.Errorf("redis time: %w", err)
	}
	return t, nil
}
