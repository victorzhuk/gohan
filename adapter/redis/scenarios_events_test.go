//go:build full

package redis_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/victorzhuk/gohan/adapter/redis"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/storetest"
)

// skewedClockClient wraps a Redis client whose TIME command answers with a
// skewed instant, so tests can observe that the store clock is the client
// TIME path and never the process clock.
type skewedClockClient struct {
	*goredis.Client
	offset time.Duration
}

func (c skewedClockClient) Time(ctx context.Context) *goredis.TimeCmd {
	cmd := goredis.NewTimeCmd(ctx)
	cmd.SetVal(time.Now().Add(c.offset))
	return cmd
}

func TestRedisEventLogScenarios(t *testing.T) {
	ctx := context.Background()
	client := newRedisClient(t)

	t.Run("stores.redis-eventlog-shared-reattach", func(t *testing.T) {
		prefix := fmt.Sprintf("%s-reattach", t.Name())
		a := redis.NewEventLog(client, redis.WithPrefix(prefix))
		b := redis.NewEventLog(client, redis.WithPrefix(prefix))
		for i := range 3 {
			if err := a.Append(ctx, "run-1", stores.Event{Payload: types.TextDelta{Turn: i}}); err != nil {
				t.Fatalf("Append #%d: %v", i+1, err)
			}
		}
		collect := func() []int64 {
			t.Helper()
			var seqs []int64
			for e, err := range b.Read(ctx, "run-1", 1) {
				if err != nil {
					t.Fatalf("Read: %v", err)
				}
				seqs = append(seqs, e.Meta.Seq)
			}
			return seqs
		}
		first := collect()
		if len(first) != 2 || first[0] != 2 || first[1] != 3 {
			t.Fatalf("first read delivered %v, want exactly [2 3]", first)
		}
		again := collect()
		if len(again) != 2 || again[0] != 2 || again[1] != 3 {
			t.Fatalf("second read delivered %v, want the same window [2 3]", again)
		}
		small := redis.NewEventLog(client, redis.WithPrefix(prefix+"-cap"), redis.WithEventLogCapacity(3))
		for i := range 10 {
			if err := small.Append(ctx, "run-1", stores.Event{Payload: types.TextDelta{Turn: i}}); err != nil {
				t.Fatalf("capacity Append #%d: %v", i+1, err)
			}
		}
		for _, err := range small.Read(ctx, "run-1", 2) {
			if err == nil {
				continue
			}
			if !errors.Is(err, stores.ErrStaleCursor) {
				t.Fatalf("Read below the retained window: %v, want stores.ErrStaleCursor", err)
			}
			return
		}
		t.Fatalf("Read below the retained window served events, want a stale-cursor refusal")
	})

	t.Run("stores.redis-eventlog-compaction-idempotent", func(t *testing.T) {
		prefix := fmt.Sprintf("%s-compaction", t.Name())
		log := redis.NewEventLog(client, redis.WithPrefix(prefix))
		pubID := stores.CompactionPublicationID("s-1", "run-1", 3)
		event := stores.Event{
			Payload: types.Compacted{FromVersion: 3, ToVersion: 9, TokensBefore: 800, TokensAfter: 90, Policy: "auto", PublicationID: pubID},
			Meta:    types.EventMeta{SessionID: "s-1", RootRunID: "run-1"},
		}
		first, err := log.AppendCompactionEvent(ctx, pubID, "run-1", event)
		if err != nil || !first {
			t.Fatalf("first AppendCompactionEvent = (%v, %v), want (true, nil)", first, err)
		}
		second, err := log.AppendCompactionEvent(ctx, pubID, "run-1", event)
		if err != nil || second {
			t.Fatalf("second AppendCompactionEvent = (%v, %v), want (false, nil)", second, err)
		}
		var delivered int
		for _, err := range log.Read(ctx, "run-1", 0) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			delivered++
		}
		if delivered != 1 {
			t.Fatalf("Read delivered %d events, want exactly the one compaction event", delivered)
		}
		other := event
		other.Payload = types.Compacted{FromVersion: 3, ToVersion: 10, PublicationID: pubID}
		if ok, err := log.AppendCompactionEvent(ctx, pubID, "run-1", other); err == nil {
			t.Fatalf("conflicting payload accepted (ok=%v), want errCompactionConflict", ok)
		}
		delivered = 0
		for _, err := range log.Read(ctx, "run-1", 0) {
			if err != nil {
				t.Fatalf("Read after conflict: %v", err)
			}
			delivered++
		}
		if delivered != 1 {
			t.Fatalf("conflict appended: Read delivered %d events, want 1", delivered)
		}
	})

	t.Run("stores.redis-conformance-parity", func(t *testing.T) {
		client := newRedisClient(t)
		var n atomic.Int64
		storetest.EventLog(t, func(t *testing.T, now func() time.Time, capacity int) storetest.EventLogStore {
			t.Helper()
			return newBoundEventLog(t, client, now, capacity, &n)
		})
		log := redis.NewEventLog(client, redis.WithPrefix(fmt.Sprintf("%s-parity", t.Name())))
		if err := log.Append(ctx, "run-1", stores.Event{Payload: types.TextDelta{Delta: "d"}}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		prefix := fmt.Sprintf("%s-parity", t.Name())
		entries, err := client.XRangeN(ctx, prefix+":events:run:run-1", "-", "+", 1).Result()
		if err != nil || len(entries) != 1 {
			t.Fatalf("stored stream entries = %d, err = %v, want one", len(entries), err)
		}
		var env stores.RecordEnvelope
		if err := json.Unmarshal([]byte(entries[0].Values["e"].(string)), &env); err != nil {
			t.Fatalf("decode stored envelope: %v", err)
		}
		if env.Shape != "Event" {
			t.Fatalf("record envelope shape = %q, want %q", env.Shape, "Event")
		}
		small := redis.NewEventLog(client, redis.WithPrefix(prefix+"-stale"), redis.WithEventLogCapacity(1))
		if err := small.Append(ctx, "run-1", stores.Event{Payload: types.TextDelta{}}); err != nil {
			t.Fatalf("stale Append: %v", err)
		}
		if err := small.Append(ctx, "run-1", stores.Event{Payload: types.TextDelta{}}); err != nil {
			t.Fatalf("stale Append: %v", err)
		}
		for _, err := range small.Read(ctx, "run-1", 0) {
			if err == nil {
				continue
			}
			if !errors.Is(err, stores.ErrStaleCursor) {
				t.Fatalf("stale refusal: %v, want stores.ErrStaleCursor", err)
			}
			return
		}
		t.Fatalf("Read below the retained window served events, want a stale-cursor refusal")
	})

	t.Run("event time comes from the server clock", func(t *testing.T) {
		for i, skew := range []time.Duration{-5 * time.Minute, 5 * time.Minute} {
			wrapped := goredis.NewClient(&goredis.Options{Addr: client.Options().Addr})
			t.Cleanup(func() { _ = wrapped.Close() })
			log := redis.NewEventLog(skewedClockClient{Client: wrapped, offset: skew},
				redis.WithPrefix(fmt.Sprintf("%s-clock%d", t.Name(), i)))
			process := time.Now()
			if err := log.Append(ctx, "run-1", stores.Event{Payload: types.TextDelta{Delta: "d"}}); err != nil {
				t.Fatalf("Append (skew %v): %v", skew, err)
			}
			var got stores.Event
			for e, err := range log.Read(ctx, "run-1", 0) {
				if err != nil {
					t.Fatalf("Read (skew %v): %v", skew, err)
				}
				got = e
				break
			}
			want := time.Now().Add(skew)
			if d := got.Meta.Time.Sub(want); d < -2*time.Second || d > 2*time.Second {
				t.Fatalf("skew %v: stored Time off the client TIME clock by %v, want within two seconds", skew, d)
			}
			if d := got.Meta.Time.Sub(process).Abs() - skew.Abs(); d > 2*time.Second {
				t.Fatalf("skew %v: stored Time tracks the process clock: %v vs %v", skew, got.Meta.Time, process)
			}
		}

		prefix := fmt.Sprintf("%s-explicit", t.Name())
		log := redis.NewEventLog(client, redis.WithPrefix(prefix))
		server, err := redis.ServerNow(ctx, client)
		if err != nil {
			t.Fatalf("ServerNow: %v", err)
		}
		explicit := server.Add(-10 * time.Minute).Round(time.Millisecond)
		if err := log.Append(ctx, "run-1", stores.Event{
			Payload: types.TextDelta{Delta: "old"},
			Meta:    types.EventMeta{Time: explicit},
		}); err != nil {
			t.Fatalf("Append with explicit Time: %v", err)
		}
		for e, err := range log.Read(ctx, "run-1", 0) {
			if err != nil {
				t.Fatalf("Read explicit: %v", err)
			}
			if !e.Meta.Time.Equal(explicit) {
				t.Fatalf("stored Time = %v, want the caller-supplied %v", e.Meta.Time, explicit)
			}
			break
		}
		if err := log.Append(ctx, "run-1", stores.Event{Payload: types.TextDelta{Delta: "new"}}); err != nil {
			t.Fatalf("Append fresh: %v", err)
		}
		if err := log.Expire(ctx, server.Add(-5*time.Minute)); err != nil {
			t.Fatalf("Expire: %v", err)
		}
		var kept []string
		for e, err := range log.Read(ctx, "run-1", 1) {
			if err != nil {
				t.Fatalf("Read after Expire: %v", err)
			}
			delta := e.Payload.(types.TextDelta)
			kept = append(kept, delta.Delta)
		}
		if len(kept) != 1 || kept[0] != "new" {
			t.Fatalf("after Expire Read delivered %v, want only the fresh event", kept)
		}
	})

	t.Run("expired runs leave the sweep index", func(t *testing.T) {
		prefix := fmt.Sprintf("%s-index", t.Name())
		log := redis.NewEventLog(client, redis.WithPrefix(prefix))
		server, err := redis.ServerNow(ctx, client)
		if err != nil {
			t.Fatalf("ServerNow: %v", err)
		}
		old := server.Add(-30 * time.Minute).Round(time.Millisecond)
		for i := range 3 {
			if err := log.Append(ctx, "run-a", stores.Event{
				Payload: types.TextDelta{Turn: i},
				Meta:    types.EventMeta{Time: old},
			}); err != nil {
				t.Fatalf("Append run-a #%d: %v", i+1, err)
			}
		}
		for i, at := range []time.Time{old, server.Add(-time.Minute).Round(time.Millisecond)} {
			if err := log.Append(ctx, "run-b", stores.Event{
				Payload: types.TextDelta{Turn: i},
				Meta:    types.EventMeta{Time: at},
			}); err != nil {
				t.Fatalf("Append run-b #%d: %v", i+1, err)
			}
		}
		index := prefix + ":events:index"
		if err := log.Expire(ctx, server.Add(-15*time.Minute)); err != nil {
			t.Fatalf("Expire: %v", err)
		}
		if _, err := client.ZScore(ctx, index, "run-a").Result(); !errors.Is(err, goredis.Nil) {
			t.Fatalf("fully expired run still indexed: score err = %v, want Nil", err)
		}
		if err := client.Exists(ctx, prefix+":events:seq:run-a").Err(); err != nil {
			t.Fatalf("Exists counter run-a: %v", err)
		}
		next, err := client.Get(ctx, prefix+":events:seq:run-a").Int64()
		if err != nil {
			t.Fatalf("counter run-a: %v", err)
		}
		served := 0
		for _, err := range log.Read(ctx, "run-a", next) {
			if err != nil {
				t.Fatalf("Read run-a at counter: %v", err)
			}
			served++
		}
		if served != 0 {
			t.Fatalf("Read run-a at counter served %d events, want none", served)
		}
		score, err := client.ZScore(ctx, index, "run-b").Result()
		if err != nil || score <= float64(server.Add(-15*time.Minute).UnixMilli()) {
			t.Fatalf("mixed run score = %v, err = %v, want a score above the cutoff", score, err)
		}
		var delivered int
		for e, err := range log.Read(ctx, "run-b", 1) {
			if err != nil {
				t.Fatalf("Read run-b: %v", err)
			}
			delta, ok := e.Payload.(types.TextDelta)
			if !ok || delta.Turn != 1 {
				t.Fatalf("run-b event %#v, want the surviving Turn 1 delta", e.Payload)
			}
			delivered++
		}
		if delivered != 1 {
			t.Fatalf("Read run-b delivered %d events, want the one survivor", delivered)
		}
	})

	t.Run("empty slice payload survives the stream", func(t *testing.T) {
		prefix := fmt.Sprintf("%s-empty", t.Name())
		log := redis.NewEventLog(client, redis.WithPrefix(prefix))
		msg := types.AssistantMessage{Message: types.Message{Blocks: []types.Block{}}}
		if err := log.Append(ctx, "run-1", stores.Event{Payload: msg}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		for e, err := range log.Read(ctx, "run-1", 0) {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			got, ok := e.Payload.(types.AssistantMessage)
			if !ok {
				t.Fatalf("payload %#v, want types.AssistantMessage", e.Payload)
			}
			if got.Message.Blocks == nil || len(got.Message.Blocks) != 0 {
				t.Fatalf("Blocks = %#v, want an empty non-nil slice", got.Message.Blocks)
			}
			return
		}
		t.Fatalf("Read served nothing")
	})
}
