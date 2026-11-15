package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/victorzhuk/gohan/core/types"
)

const spendScript = `
local v = redis.call('INCRBYFLOAT', KEYS[1], ARGV[1])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return v
`

const dayFormat = "2006-01-02"

// Spend records the cost in the day accumulator of the store clock. It
// returns the day limit error only when the recorded spend exceeds
// DailyLimit, while Admit refuses at >= DailyLimit: recording a cost that
// lands exactly on the bound is not an overrun, and admission at the bound
// refuses the next call.
func (p *Pool) Spend(ctx context.Context, cost float64) error {
	nowMS, err := p.c.nowMS(ctx, p.client)
	if err != nil {
		return fmt.Errorf("pool spend: %w", err)
	}
	day := time.UnixMilli(nowMS).UTC().Format(dayFormat)
	raw, err := p.client.Eval(
		ctx, spendScript, []string{p.c.key("pool", p.cfg.Name, "spend", day)},
		strconv.FormatFloat(cost, 'f', -1, 64),
		strconv.FormatInt(48*time.Hour.Milliseconds(), 10),
	).Result()
	if err != nil {
		return fmt.Errorf("pool spend: %w", err)
	}
	spend, err := strconv.ParseFloat(fmt.Sprint(raw), 64)
	if err != nil {
		return fmt.Errorf("pool spend: decode accumulator: %w", err)
	}

	if baseline, err := p.baseline(ctx, nowMS); err == nil && baseline > 0 {
		factor := p.cfg.AnomalyFactor
		if factor <= 0 {
			factor = 3
		}
		if spend > factor*baseline {
			if hook := p.c.telemetry.CostAnomaly; hook != nil {
				hook()
			}
		}
	}

	if p.cfg.DailyLimit > 0 && spend > p.cfg.DailyLimit {
		return &types.LimitExceededError{Limit: "pool/day", Value: spend}
	}
	return nil
}

// baseline averages the previous seven UTC days' accumulators; days with
// no recorded spend are skipped. 0 means no baseline exists.
func (p *Pool) baseline(ctx context.Context, nowMS int64) (float64, error) {
	today := time.UnixMilli(nowMS).UTC()
	keys := make([]string, 0, 7)
	for i := 1; i <= 7; i++ {
		keys = append(keys, p.c.key("pool", p.cfg.Name, "spend", today.AddDate(0, 0, -i).Format(dayFormat)))
	}
	raws, err := p.client.MGet(ctx, keys...).Result()
	if err != nil {
		return 0, err
	}
	sum, count := 0.0, 0
	for _, raw := range raws {
		s, ok := raw.(string)
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("pool spend: decode accumulator: %w", err)
		}
		sum += value
		count++
	}
	if count == 0 {
		return 0, nil
	}
	return sum / float64(count), nil
}

func (p *Pool) dayValue(ctx context.Context, key string) (float64, error) {
	raw, err := p.client.Get(ctx, key).Result()
	if err != nil {
		if err == goredis.Nil {
			return 0, nil
		}
		return 0, err
	}
	return strconv.ParseFloat(raw, 64)
}

func (p *Pool) dailySpend(ctx context.Context, nowMS int64) (float64, error) {
	day := time.UnixMilli(nowMS).UTC().Format(dayFormat)
	value, err := p.dayValue(ctx, p.c.key("pool", p.cfg.Name, "spend", day))
	if err != nil {
		return 0, fmt.Errorf("read day accumulator: %w", err)
	}
	return value, nil
}

// DailySpend returns the recorded spend for the store clock's UTC day, 0
// when nothing is recorded yet.
func (p *Pool) DailySpend(ctx context.Context) (float64, error) {
	nowMS, err := p.c.nowMS(ctx, p.client)
	if err != nil {
		return 0, fmt.Errorf("pool spend: %w", err)
	}
	value, err := p.dailySpend(ctx, nowMS)
	if err != nil {
		return 0, fmt.Errorf("pool spend: %w", err)
	}
	return value, nil
}
