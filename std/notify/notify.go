// Package notify delivers the runs notice outbox to the stack's Notifier.
// Delivery is at-least-once and unordered: the notice ID is the consumer's
// idempotency key. A delivery that does not succeed stays pending and is
// retried with exponential backoff bounded by a 72 hour budget, after
// which the notice is dropped with a notice_dead audit record.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/notify/webhook"
)

const (
	metricDelivered = "gohan.notice.delivered"
	metricRetried   = "gohan.notice.retried"
	metricDead      = "gohan.notice.dead"
	metricUnrouted  = "gohan.notice.unrouted"
	metricEgress    = "gohan.egress.requests"
)

const (
	// RetryStart and RetryCap bound the exponential backoff: one second
	// doubling to one hour.
	RetryStart = time.Second
	RetryCap   = time.Hour
	// DeadLetterAfter is the total retry budget. A notice the budget
	// exhausts is dropped with the notice_dead audit record, never retried.
	DeadLetterAfter = 72 * time.Hour
	// claimBatch bounds one Deliver pass's claim; the poll cadence of the
	// Run loop.
	claimBatch = 16
	pollEvery  = time.Second
)

// Dispatcher claims pending notices from the runs store and hands each to
// the stack's Notifier, acknowledging only on success.
type Dispatcher struct {
	runs     stores.Runs
	sink     types.Notifier
	client   *http.Client
	tel      types.Telemetry
	logger   *slog.Logger
	clock    func() time.Time
	only     map[types.NoticeKind]bool
	schedule map[string]*attempt
}

// attempt is one notice's retry state: when the budget started and when
// the next delivery is due.
type attempt struct {
	first time.Time
	next  time.Time
	tries int
}

// Option adjusts Dispatcher.
type Option func(*Dispatcher)

// Only restricts delivery to the named notice kinds. An unlisted kind
// stays pending in the outbox.
func Only(kinds ...types.NoticeKind) Option {
	return func(d *Dispatcher) {
		d.only = make(map[types.NoticeKind]bool, len(kinds))
		for _, k := range kinds {
			d.only[k] = true
		}
	}
}

// WithTelemetry binds the counter sink for gohan.notice.* and the notice
// egress metric. A telemetry bound to ctx wins per call.
func WithTelemetry(tel types.Telemetry) Option {
	return func(d *Dispatcher) { d.tel = tel }
}

// WithLogger binds the delivery failure log. The default discards.
func WithLogger(logger *slog.Logger) Option {
	return func(d *Dispatcher) { d.logger = logger }
}

// WithClock replaces the retry clock, so tests drive the backoff without
// waiting.
func WithClock(now func() time.Time) Option {
	return func(d *Dispatcher) { d.clock = now }
}

// New builds the dispatcher over the runs outbox, the stack's sink and the
// client the stack resolved from its EgressPolicy. The sender posts every
// notice through that client.
func New(runs stores.Runs, sink types.Notifier, client *http.Client, opts ...Option) *Dispatcher {
	d := &Dispatcher{
		runs:     runs,
		sink:     sink,
		client:   client,
		logger:   slog.New(slog.DiscardHandler),
		clock:    time.Now,
		schedule: make(map[string]*attempt),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Deliver runs one deterministic pass: claim a batch, deliver each due
// notice, acknowledge the ones that succeeded. The pass is what tests and
// the host clock drive; Run wraps it in a loop.
func (d *Dispatcher) Deliver(ctx context.Context) error {
	if d.sink == nil || d.runs == nil {
		return nil
	}
	notices, err := d.runs.Notices(ctx, claimBatch)
	if err != nil {
		return fmt.Errorf("claim notices: %w", err)
	}
	now := d.clock()
	for _, notice := range notices {
		if len(d.only) > 0 && !d.only[notice.Kind] {
			continue
		}
		if att, ok := d.schedule[notice.ID]; ok && now.Before(att.next) {
			continue
		}
		if err := d.deliver(ctx, notice, now); err != nil {
			return err
		}
	}
	return nil
}

// Run loops Deliver until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		if err := d.Deliver(ctx); err != nil {
			d.logger.Error("notice delivery pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *Dispatcher) deliver(ctx context.Context, notice types.RunNotice, now time.Time) error {
	err := d.sink.Notify(ctx, notice)
	switch {
	case err == nil:
		d.count(ctx, metricDelivered, 1, types.String(types.KeyNoticeKind, notice.Kind.String()))
		return d.settle(ctx, notice)
	case errors.Is(err, webhook.ErrUnrouted):
		d.count(ctx, metricUnrouted, 1, types.String("tenant", notice.Tenant))
		return d.settle(ctx, notice)
	}
	var denied types.EgressDenied
	if errors.As(err, &denied) {
		d.refuse(ctx, notice, denied)
		return d.settle(ctx, notice)
	}
	return d.retry(ctx, notice, err, now)
}

// settle acknowledges the notice, ending its retry state with it.
func (d *Dispatcher) settle(ctx context.Context, notice types.RunNotice) error {
	delete(d.schedule, notice.ID)
	if err := d.runs.AckNotice(ctx, notice.ID); err != nil {
		return fmt.Errorf("ack notice %s: %w", notice.ID, err)
	}
	return nil
}

// retry schedules the next attempt on the one backoff schedule; a rate
// limit honors the endpoint's Retry-After instead. The 72 hour budget is
// judged from the first failure: past it the notice is dropped with the
// notice_dead audit record.
func (d *Dispatcher) retry(ctx context.Context, notice types.RunNotice, err error, now time.Time) error {
	att := d.schedule[notice.ID]
	if att == nil {
		att = &attempt{first: now}
	}
	att.tries++
	if now.Sub(att.first) >= DeadLetterAfter {
		if audit, ok := gohan.AuditLogFrom(ctx); ok {
			rec := stores.AuditRecord{
				Kind:      stores.AuditNoticeDead,
				Tenant:    notice.Tenant,
				SessionID: notice.SessionID,
				RunID:     notice.RunID,
				Reason:    notice.ID,
			}
			if aerr := audit.Append(ctx, rec); aerr != nil {
				d.logger.Error("notice dead-letter audit failed", "notice", notice.ID, "err", aerr)
			}
		}
		d.count(ctx, metricDead, 1, types.String(types.KeyNoticeKind, notice.Kind.String()))
		return d.settle(ctx, notice)
	}
	att.next = now.Add(d.delay(err, att.tries))
	d.schedule[notice.ID] = att
	d.count(ctx, metricRetried, 1, types.String(types.KeyNoticeKind, notice.Kind.String()))
	d.logger.Warn("notice delivery failed", "notice", notice.ID, "tries", att.tries, "next", att.next.Sub(now), "err", err)
	return nil
}

// refuse records an egress refusal the policy issued before any
// connection: the audit record carries host and reason only, and the
// notice is acknowledged as undeliverable rather than retried. The 72
// hour notice_dead budget stays reserved for delivery attempts.
func (d *Dispatcher) refuse(ctx context.Context, notice types.RunNotice, denied types.EgressDenied) {
	host := hostOf(denied.URL)
	if audit, ok := gohan.AuditLogFrom(ctx); ok {
		rec := stores.AuditRecord{
			Kind:      stores.AuditEgressDenied,
			Tenant:    notice.Tenant,
			SessionID: notice.SessionID,
			RunID:     notice.RunID,
			Host:      host,
			Reason:    denied.Reason,
		}
		if aerr := audit.Append(ctx, rec); aerr != nil {
			d.logger.Error("notice egress audit failed", "notice", notice.ID, "err", aerr)
		}
	}
	d.count(ctx, metricEgress, 1,
		types.String("tool", "notice"),
		types.String("host", host),
		types.String("decision", "denied"))
}

func (d *Dispatcher) delay(err error, tries int) time.Duration {
	var merr *types.ModelError
	if errors.As(err, &merr) && merr.Class == types.ClassRateLimited && merr.RetryAfter > 0 {
		return merr.RetryAfter
	}
	backoff := RetryStart
	for i := 1; i < tries && backoff < RetryCap; i++ {
		backoff *= 2
	}
	return min(backoff, RetryCap)
}

func (d *Dispatcher) count(ctx context.Context, name string, n int64, attrs ...types.Attr) {
	tel := d.tel
	if tel == nil {
		tel = types.TelemetryFrom(ctx)
	}
	if tel != nil {
		tel.Count(ctx, name, n, attrs...)
	}
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
