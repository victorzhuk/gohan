package redis

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/victorzhuk/gohan/core/types"
)

// ErrPoolExhausted reports that the class bucket has no token; RetryAfter
// carries the refill horizon for one token.
var ErrPoolExhausted = errors.New("gohan: quota pool exhausted")

// ErrDeferred reports that a Batch admission was deferred while the
// interactive queue-wait exceeded the configured threshold. RetryAfter is
// the deterministic horizon until the oldest in-window sample leaves the
// window; jittering it is the retry policy's job.
var ErrDeferred = errors.New("gohan: batch admission deferred")

// ClassShares splits the pool limit across latency classes.
type ClassShares struct {
	Interactive float64
	Agentic     float64
	Batch       float64
}

// DefaultClassShares returns the 0.5 / 0.3 / 0.2 split.
func DefaultClassShares() ClassShares {
	return ClassShares{Interactive: 0.5, Agentic: 0.3, Batch: 0.2}
}

// PoolConfig describes one shared quota pool.
type PoolConfig struct {
	Name          string
	Limit         int64
	Window        time.Duration
	Shares        ClassShares
	QueueWait     time.Duration
	DailyLimit    float64
	AnomalyFactor float64
}

// Admission is one admission attempt for a latency class.
type Admission struct {
	Class  types.LatencyClass
	Waited time.Duration
}

// Decision reports the retry horizon and the measured interactive queue-wait
// at decision time.
type Decision struct {
	RetryAfter time.Duration
	QueueWait  time.Duration
}

// Pool is the shared quota pool limiter on Redis.
type Pool struct {
	cfg    PoolConfig
	c      *config
	client goredis.UniversalClient
}

// Token bucket per class. Server TIME is read inside the script so no
// caller's clock can decide a refill; the retry value is the time to one
// token, not the window remainder.
const bucketScript = `
local t = redis.call('TIME')
local nowms = tonumber(t[1])*1000 + math.floor(tonumber(t[2])/1000)
local cap = tonumber(ARGV[1])
local windowms = tonumber(ARGV[2])
local rate = cap / windowms
if rate <= 0 then
	redis.call('PEXPIRE', KEYS[1], ARGV[3])
	return {0, windowms, nowms}
end
local b = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens, ts = tonumber(b[1]), tonumber(b[2])
if tokens == nil then tokens, ts = cap, nowms end
if nowms > ts then
	tokens = math.min(cap, tokens + (nowms - ts) * rate)
	ts = nowms
end
local admitted, retry = 1, 0
if tokens >= 1 then tokens = tokens - 1 else
	admitted = 0
	retry = math.ceil((1 - tokens) / rate)
end
redis.call('HSET', KEYS[1], 'tokens', tostring(tokens), 'ts', tostring(ts))
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return {admitted, retry, nowms}
`

// Queue-wait samples live in one sorted set per pool. The caller's clock
// seam (c.nowMS) supplies nowms so tests can drive the window; the member
// carries the wait because the score alone only stamps the arrival time.
// Format: <waitedNanos>:<seq>.
const waitScript = `
local nowms = tonumber(ARGV[1])
local windowms = tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', nowms - windowms)
if ARGV[3] ~= '' then
	redis.call('ZADD', KEYS[1], nowms, ARGV[3])
end
redis.call('ZREMRANGEBYRANK', KEYS[1], 0, -1025)
redis.call('PEXPIRE', KEYS[1], ARGV[4])
local count, sum = 0, 0
for _, m in ipairs(redis.call('ZRANGEBYSCORE', KEYS[1], nowms - windowms, '+inf')) do
	local nanos = tonumber(string.match(m, '^(-?%d+):'))
	if nanos then
		count = count + 1
		sum = sum + nanos
	end
end
local oldest = 0
local low = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
if low[2] then oldest = tonumber(low[2]) end
return {nowms, count, sum, oldest}
`

var waitSeq atomic.Uint64

func classKey(class types.LatencyClass) string {
	switch class {
	case types.Agentic:
		return "agentic"
	case types.Batch:
		return "batch"
	default:
		return "interactive"
	}
}

// classCaps computes per-class admission caps. Batch and Agentic floors
// give their rounding surpluses back to Interactive, so the caps always sum
// to the limit exactly, Interactive keeps at least its share, and Batch is
// reduced before Agentic when both fractions compete for the remainder.
func classCaps(limit int64, shares ClassShares) (int64, int64, int64) {
	batch := int64(math.Floor(shares.Batch * float64(limit)))
	if batch < 0 {
		batch = 0
	}
	agentic := int64(math.Floor(shares.Agentic * float64(limit)))
	if agentic < 0 {
		agentic = 0
	}
	interactive := limit - agentic - batch
	if interactive < 0 {
		interactive = 0
	}
	return interactive, agentic, batch
}

// NewPool validates the config and returns a pool on the given client.
func NewPool(client goredis.UniversalClient, cfg PoolConfig, opts ...Option) (*Pool, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	c := defaultConfig()
	for _, opt := range opts {
		opt(c)
	}
	return &Pool{cfg: cfg, c: c, client: client}, nil
}

func (cfg PoolConfig) validate() error {
	if cfg.Name == "" {
		return fmt.Errorf("pool %q: name is empty", cfg.Name)
	}
	if cfg.Limit <= 0 {
		return fmt.Errorf("pool %q: limit must be positive", cfg.Name)
	}
	if cfg.Window < time.Millisecond {
		return fmt.Errorf("pool %q: window below 1ms", cfg.Name)
	}
	if cfg.QueueWait < 0 {
		return fmt.Errorf("pool %q: queue wait is negative", cfg.Name)
	}
	if cfg.AnomalyFactor < 0 {
		return fmt.Errorf("pool %q: anomaly factor is negative", cfg.Name)
	}
	shares := []struct {
		name  string
		value float64
	}{
		{"interactive share", cfg.Shares.Interactive},
		{"agentic share", cfg.Shares.Agentic},
		{"batch share", cfg.Shares.Batch},
	}
	anySet := false
	for _, s := range shares {
		if s.value != 0 {
			anySet = true
		}
		if s.value < 0 || s.value > 1 {
			return fmt.Errorf("pool %q: %s %g outside [0,1]", cfg.Name, s.name, s.value)
		}
	}
	if anySet {
		sum := cfg.Shares.Interactive + cfg.Shares.Agentic + cfg.Shares.Batch
		if math.Abs(sum-1) > 1e-9 {
			return fmt.Errorf("pool %q: shares sum to %g, want 1", cfg.Name, sum)
		}
	}
	return nil
}

func (p *Pool) shares() ClassShares {
	if p.cfg.Shares == (ClassShares{}) {
		return DefaultClassShares()
	}
	return p.cfg.Shares
}

func encodeWaitSample(waited time.Duration) string {
	return strconv.FormatInt(int64(waited), 10) + ":" + strconv.FormatUint(waitSeq.Add(1), 10)
}

// intsReply decodes a Lua reply expected to be a flat array of integers.
func intsReply(raw any, op string, n int) ([]int64, error) {
	arr, ok := raw.([]any)
	if !ok || len(arr) != n {
		return nil, fmt.Errorf("%s: decode reply: want %d integers, got %T", op, n, raw)
	}
	out := make([]int64, n)
	for i, v := range arr {
		x, ok := v.(int64)
		if !ok {
			return nil, fmt.Errorf("%s: decode reply: element %d is %T, want integer", op, i, v)
		}
		out[i] = x
	}
	return out, nil
}

// Admit runs the decision chain for one admission attempt.
func (p *Pool) Admit(ctx context.Context, a Admission) (Decision, error) {
	const op = "pool admit"
	nowMS, err := p.c.nowMS(ctx, p.client)
	if err != nil {
		return Decision{}, fmt.Errorf("%s: %w", op, err)
	}
	windowMS := p.cfg.Window.Milliseconds()

	if p.cfg.DailyLimit > 0 {
		spend, err := p.dailySpend(ctx, nowMS)
		if err != nil {
			return Decision{}, fmt.Errorf("%s: %w", op, err)
		}
		if spend >= p.cfg.DailyLimit {
			return Decision{}, &types.LimitExceededError{Limit: "pool/day", Value: spend}
		}
	}

	member := ""
	if a.Class == types.Interactive && a.Waited > 0 {
		member = encodeWaitSample(a.Waited)
	}
	raw, err := p.client.Eval(
		ctx, waitScript, []string{p.c.key("pool", p.cfg.Name, "wait")},
		strconv.FormatInt(nowMS, 10),
		strconv.FormatInt(windowMS, 10),
		member,
		strconv.FormatInt(2*windowMS, 10),
	).Result()
	if err != nil {
		return Decision{}, fmt.Errorf("%s: %w", op, err)
	}
	vals, err := intsReply(raw, op, 4)
	if err != nil {
		return Decision{}, fmt.Errorf("%s: %w", op, err)
	}
	_, count, sumNanos, oldest := vals[0], vals[1], vals[2], vals[3]

	var mean time.Duration
	if count > 0 {
		mean = time.Duration(sumNanos / count)
		if hook := p.c.telemetry.QueueWait; hook != nil {
			hook(mean)
		}
	}

	if a.Class == types.Batch && p.cfg.QueueWait > 0 && mean > p.cfg.QueueWait {
		retry := windowMS
		if count > 0 {
			retry = oldest + windowMS - nowMS
			if retry < 1 {
				retry = 1
			}
			if retry > windowMS {
				retry = windowMS
			}
		}
		if hook := p.c.telemetry.Deferred; hook != nil {
			hook(a.Class)
		}
		return Decision{RetryAfter: time.Duration(retry) * time.Millisecond, QueueWait: mean}, ErrDeferred
	}

	interactive, agentic, batch := classCaps(p.cfg.Limit, p.shares())
	admitCap := interactive
	switch a.Class {
	case types.Agentic:
		admitCap = agentic
	case types.Batch:
		admitCap = batch
	}

	raw, err = p.client.Eval(
		ctx, bucketScript, []string{p.c.key("pool", p.cfg.Name, "bucket", classKey(a.Class))},
		strconv.FormatInt(admitCap, 10),
		strconv.FormatInt(windowMS, 10),
		strconv.FormatInt(2*windowMS, 10),
	).Result()
	if err != nil {
		return Decision{}, fmt.Errorf("%s: %w", op, err)
	}
	vals, err = intsReply(raw, op, 3)
	if err != nil {
		return Decision{}, fmt.Errorf("%s: %w", op, err)
	}
	if vals[0] == 0 {
		return Decision{RetryAfter: time.Duration(vals[1]) * time.Millisecond, QueueWait: mean}, ErrPoolExhausted
	}
	return Decision{QueueWait: mean}, nil
}
