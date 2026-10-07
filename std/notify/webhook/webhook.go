// Package webhook delivers run notices as signed Standard Webhooks POSTs.
// Every request rides the http.Client the stack resolved from its
// EgressPolicy; the package opens no connection of its own.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// ErrUnrouted reports a tenant without an endpoint. The dispatcher
// acknowledges the notice instead of retrying it.
var ErrUnrouted = errors.New("gohan: no notice endpoint for tenant")

// Endpoint resolves one tenant's destination and secret. It is called with
// the notice's own tenant only: a delivery never reads another tenant's
// entry or a default.
type Endpoint func(tenant string) (url, secret string, ok bool)

const signatureVersion = "v1"

// Notifier is the Standard Webhooks types.Notifier.
type Notifier struct {
	endpoint Endpoint
	client   *http.Client
	now      func() time.Time
}

// Option adjusts Notifier.
type Option func(*Notifier)

// WithClock replaces the send clock, which bounds the
// webhook-timestamp the receiver verifies.
func WithClock(now func() time.Time) Option {
	return func(n *Notifier) { n.now = now }
}

// New builds the notifier over the endpoint registry and the stack's
// egress client.
func New(endpoint Endpoint, client *http.Client, opts ...Option) *Notifier {
	n := &Notifier{endpoint: endpoint, client: client, now: time.Now}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

// Notify POSTs the notice JSON to the tenant's endpoint, signed over
// id.timestamp.body with the tenant's secret.
func (n *Notifier) Notify(ctx context.Context, notice types.RunNotice) error {
	endpoint, secret, ok := n.endpoint(notice.Tenant)
	if !ok || endpoint == "" || secret == "" {
		return ErrUnrouted
	}
	body, err := json.Marshal(notice)
	if err != nil {
		return &types.ModelError{Class: types.ClassPermanent, Err: err}
	}
	timestamp := n.now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return &types.ModelError{Class: types.ClassPermanent, Err: err}
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("webhook-id", notice.ID)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("webhook-signature", Sign(secret, notice.ID, timestamp, body))
	resp, err := n.client.Do(req)
	if err != nil {
		var denied types.EgressDenied
		if errors.As(err, &denied) {
			return denied
		}
		return Classify(0, 0, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return Classify(resp.StatusCode, retryAfter(resp.Header.Get("retry-after")), nil)
}

// Sign computes the Standard Webhooks signature header value: v1, followed
// by base64 of HMAC-SHA256 over id.timestamp.body with the secret's
// base64 payload behind an optional whsec_ prefix.
func Sign(secret string, id string, timestamp int64, body []byte) string {
	key := strings.TrimPrefix(secret, "whsec_")
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		decoded = []byte(key)
	}
	mac := hmac.New(sha256.New, decoded)
	_, _ = fmt.Fprintf(mac, "%s.%d.", id, timestamp)
	mac.Write(body)
	return signatureVersion + "," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Classify maps a non-2xx delivery outcome to the model error the
// dispatcher schedules retries from. Only a 2xx delivers: 429 is
// rate-limited, 408, 5xx and transport failure are transient, and every
// other status is permanent.
func Classify(status int, retryAfter time.Duration, err error) *types.ModelError {
	if err == nil {
		err = fmt.Errorf("notice delivery status %d", status)
	}
	m := &types.ModelError{Status: status, RetryAfter: retryAfter, Err: err}
	switch {
	case status == http.StatusTooManyRequests:
		m.Class = types.ClassRateLimited
	case status == http.StatusRequestTimeout || status == 0 || status >= 500:
		m.Class = types.ClassTransient
	default:
		m.Class = types.ClassPermanent
	}
	return m
}

func retryAfter(header string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
