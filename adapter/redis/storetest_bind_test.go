//go:build full

package redis_test

import (
	"context"
	"fmt"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/victorzhuk/gohan/adapter/redis"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/testkit/storetest"
)

type eventLogBinding struct{ s stores.EventLog }

func (b eventLogBinding) Append(ctx context.Context, runID string, e storetest.Event) error {
	return b.s.Append(ctx, runID, stores.Event(e))
}

func (b eventLogBinding) Read(ctx context.Context, runID string, afterSeq int64) iter.Seq2[storetest.Event, error] {
	return func(yield func(storetest.Event, error) bool) {
		for e, err := range b.s.Read(ctx, runID, afterSeq) {
			if err != nil {
				yield(storetest.Event{}, err)
				return
			}
			if !yield(storetest.Event(e), nil) {
				return
			}
		}
	}
}

func (b eventLogBinding) Expire(ctx context.Context, olderThan time.Time) error {
	return b.s.Expire(ctx, olderThan)
}

func newBoundEventLog(t *testing.T, client *goredis.Client, now func() time.Time, capacity int, n *atomic.Int64) storetest.EventLogStore {
	t.Helper()
	prefix := fmt.Sprintf("%s-%d", t.Name(), n.Add(1))
	return eventLogBinding{s: redis.NewEventLog(client,
		redis.WithEventLogCapacity(capacity),
		redis.WithClock(now),
		redis.WithPrefix(prefix),
	)}
}

func TestEventLog(t *testing.T) {
	client := newRedisClient(t)
	var n atomic.Int64
	storetest.EventLog(t, func(t *testing.T, now func() time.Time, capacity int) storetest.EventLogStore {
		t.Helper()
		return newBoundEventLog(t, client, now, capacity, &n)
	})
}
