package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// verify recomputes the signature the way a receiver holding the plain
// secret does.
func verify(secret, id string, timestamp int64, body []byte, signature string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id))
	mac.Write([]byte("."))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return signature == want
}

func readAll(r *http.Request) []byte {
	raw, _ := io.ReadAll(r.Body)
	return raw
}

func TestWebhookSign(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("tenant-secret"))
	id := "nt-1"
	timestamp := int64(1_760_000_000)
	body := []byte(`{"id":"nt-1","kind":"finished"}`)
	got := Sign("whsec_"+secret, id, timestamp, body)
	if !verify("tenant-secret", id, timestamp, body, got) {
		t.Fatalf("Sign() = %q, does not verify over id.timestamp.body", got)
	}
}

func TestWebhookNotifies(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("s3cret"))
	var gotID, gotTS, gotSig string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get("webhook-id")
		gotTS = r.Header.Get("webhook-timestamp")
		gotSig = r.Header.Get("webhook-signature")
		gotBody = readAll(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	base := time.Unix(1_760_000_000, 0)
	n := New(func(string) (string, string, bool) { return server.URL, secret, true }, server.Client(), WithClock(func() time.Time { return base }))
	notice := types.RunNotice{ID: "nt-9", Kind: types.NoticeFinished, Tenant: "acme", SessionID: "s-1", RunID: "r-1", Reason: "completed", At: base}
	if err := n.Notify(context.Background(), notice); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	ts, _ := strconv.ParseInt(gotTS, 10, 64)
	if gotID != "nt-9" {
		t.Fatalf("webhook-id = %q, want nt-9", gotID)
	}
	if ts != base.Unix() {
		t.Fatalf("webhook-timestamp = %d, want %d", ts, base.Unix())
	}
	if !verify("s3cret", gotID, ts, gotBody, gotSig) {
		t.Fatalf("signature %q does not verify over id.timestamp.body", gotSig)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		status int
		class  types.ErrorClass
	}{
		{429, types.ClassRateLimited},
		{408, types.ClassTransient},
		{500, types.ClassTransient},
		{503, types.ClassTransient},
		{0, types.ClassTransient},
		{301, types.ClassPermanent},
		{400, types.ClassPermanent},
		{404, types.ClassPermanent},
	}
	for _, c := range cases {
		m := Classify(c.status, 0, nil)
		if m.Class != c.class {
			t.Errorf("Classify(%d).Class = %v, want %v", c.status, m.Class, c.class)
		}
	}
	m := Classify(429, 7*time.Second, nil)
	if m.RetryAfter != 7*time.Second {
		t.Fatalf("RetryAfter = %v, want 7s", m.RetryAfter)
	}
}

func TestNotifyClassifiesStatuses(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("k"))
	cases := map[int]types.ErrorClass{
		http.StatusTooManyRequests:  types.ClassRateLimited,
		http.StatusGatewayTimeout:   types.ClassTransient,
		http.StatusMovedPermanently: types.ClassPermanent,
		http.StatusForbidden:        types.ClassPermanent,
	}
	for status, want := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		n := New(func(string) (string, string, bool) { return server.URL, secret, true }, server.Client())
		err := n.Notify(context.Background(), types.RunNotice{ID: "nt", Tenant: "acme"})
		var merr *types.ModelError
		if !errors.As(err, &merr) || merr.Class != want {
			t.Errorf("status %d: err = %v, want class %v", status, err, want)
		}
		server.Close()
	}
}

func TestNotifyUnrouted(t *testing.T) {
	n := New(func(string) (string, string, bool) { return "", "", false }, http.DefaultClient)
	if err := n.Notify(context.Background(), types.RunNotice{Tenant: "acme"}); !errors.Is(err, ErrUnrouted) {
		t.Fatalf("err = %v, want ErrUnrouted", err)
	}
}
