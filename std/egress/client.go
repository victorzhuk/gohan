package egress

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type config struct {
	proxy   *url.URL
	timeout time.Duration
}

type Option func(*config)

var ErrEgressProxyUnsupported = errors.New("egress proxy scheme is unsupported")

var resolveHost = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

var dialAddress = (&net.Dialer{}).DialContext

func WithProxy(proxyURL *url.URL) Option {
	return func(c *config) { c.proxy = proxyURL }
}

func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

func Client(p types.EgressPolicy, opts ...Option) *http.Client {

	var c config
	for _, opt := range opts {
		opt(&c)
	}
	if len(p.Schemes) == 0 {
		p.Schemes = []string{"https"}
	}
	if p.PrivateRanges != types.AllowPrivate {
		p.PrivateRanges = types.DenyPrivate
	}
	if p.MaxRedirects <= 0 {
		p.MaxRedirects = 3
	}
	if p.MaxBytes <= 0 {
		p.MaxBytes = 8 << 20
	}
	if p.Timeout > 0 {
		c.timeout = p.Timeout
	}
	tr := &transport{policy: p, cfg: c}
	client := &http.Client{Transport: tr}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > p.MaxRedirects {
			return tr.denied(req.Context(), req.URL, "scheme not allowed")
		}
		if len(via) > 0 && !strings.EqualFold(via[len(via)-1].URL.Hostname(), req.URL.Hostname()) {
			req.Header.Del("Authorization")
		}
		return tr.validate(req.Context(), req.URL)
	}
	return client
}

type transport struct {
	policy types.EgressPolicy
	cfg    config
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if t.cfg.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.cfg.timeout)
		defer cancel()
		req = req.WithContext(ctx)
	}
	ips, err := t.validatedIPs(ctx, req.URL)
	if err != nil {
		return nil, err
	}
	base := http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if t.cfg.proxy != nil {
		if t.cfg.proxy.Scheme != "http" && t.cfg.proxy.Scheme != "https" {
			return nil, errors.Join(t.denied(req.Context(), req.URL, "proxy scheme not supported"), ErrEgressProxyUnsupported)
		}
		base.Proxy = http.ProxyURL(t.cfg.proxy)
		base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return t.dialChecked(ctx, network, address, req.URL)
		}
	} else {
		port := req.URL.Port()
		if port == "" {
			port = "443"
			if req.URL.Scheme == "http" {
				port = "80"
			}
		}
		ip := ips[0].IP
		base.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialAddress(ctx, network, net.JoinHostPort(ip.String(), port))
		}
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(req.Context().Err(), context.DeadlineExceeded) {
			return nil, t.denied(req.Context(), req.URL, "timeout")
		}
		return nil, err
	}
	if resp.ContentLength > t.policy.MaxBytes {
		_ = resp.Body.Close()
		return nil, t.denied(req.Context(), req.URL, "size limit "+strconv.FormatInt(t.policy.MaxBytes, 10))
	}
	resp.Body = &limitedBody{
		ReadCloser: resp.Body,
		limit:      t.policy.MaxBytes,
		deny: func() error {
			return t.denied(req.Context(), req.URL, "size limit "+strconv.FormatInt(t.policy.MaxBytes, 10))
		},
	}
	return resp, nil
}

func (t *transport) validate(ctx context.Context, u *url.URL) error {
	return t.validateRedirect(ctx, u)
}

func (t *transport) validateRedirect(ctx context.Context, u *url.URL) error {
	schemeOK := false
	for _, scheme := range t.policy.Schemes {
		if strings.EqualFold(scheme, u.Scheme) {
			schemeOK = true
			break
		}
	}
	if !schemeOK {
		return t.denied(ctx, u, "scheme not allowed")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return t.denied(ctx, u, "host not allowed")
	}
	if ip := net.ParseIP(host); ip != nil && literalAllowed(host, u.Port(), t.policy.Allow) {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && deniedAddress(ip, t.policy.PrivateRanges) {
		return t.denied(ctx, u, addressReason(ip))
	}
	if !hostAllowed(host, u.Port(), t.policy.Allow) {
		return t.denied(ctx, u, "host not allowed")
	}
	return nil
}

func addressReason(ip net.IP) string {
	if ip.IsLoopback() {
		return "loopback"
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return "link-local"
	}
	return "private range"
}

func literalAllowed(host, port string, patterns []string) bool {
	for _, pattern := range patterns {
		if !strings.ContainsAny(pattern, "*?[") && hostAllowed(host, port, []string{pattern}) {
			return true
		}
	}
	return false
}

func (t *transport) validatedIPs(ctx context.Context, u *url.URL) ([]net.IPAddr, error) {
	if err := t.validateRedirect(ctx, u); err != nil {
		return nil, err
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		return []net.IPAddr{{IP: ip}}, nil
	}
	ips, err := resolveHost(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, t.denied(ctx, u, "host not allowed")
	}
	for _, addr := range ips {
		if deniedAddress(addr.IP, t.policy.PrivateRanges) {
			if literalAllowed(host, u.Port(), t.policy.Allow) && net.ParseIP(host) != nil {
				continue
			}
			return nil, t.denied(ctx, u, addressReason(addr.IP))
		}
	}
	return ips, nil
}

func (t *transport) dialChecked(ctx context.Context, network, address string, u *url.URL) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := resolveHost(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, t.denied(ctx, u, "host not allowed")
	}
	for _, addr := range ips {
		if deniedAddress(addr.IP, t.policy.PrivateRanges) {
			return nil, t.denied(ctx, u, addressReason(addr.IP))
		}
	}
	return dialAddress(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

func deniedAddress(ip net.IP, ranges types.PrivateRanges) bool {
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ranges == types.AllowPrivate {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast()
}

func hostAllowed(host, port string, patterns []string) bool {
	for _, pattern := range patterns {
		pHost, pPort := pattern, ""
		if i := strings.LastIndex(pattern, ":"); i >= 0 && !strings.Contains(pattern[i+1:], "*") {
			pHost, pPort = pattern[:i], pattern[i+1:]
		}
		if pPort != "" && pPort != port {
			continue
		}
		if ok, _ := path.Match(strings.ToLower(pHost), host); ok {
			return true
		}
	}
	return false
}

func (t *transport) denied(_ context.Context, u *url.URL, reason string) error {
	return types.EgressDenied{URL: u.String(), Reason: reason}
}

type limitedBody struct {
	io.ReadCloser
	limit int64
	read  int64
	done  bool
	deny  func() error
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.done {
		return 0, io.EOF
	}
	if b.read >= b.limit {
		var probe [1]byte
		n, err := b.ReadCloser.Read(probe[:])
		if n > 0 {
			_ = b.Close()
			return 0, b.deny()
		}
		if err != nil {
			b.done = true
		}
		return 0, err
	}
	remaining := b.limit - b.read
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.read += int64(n)
	return n, err
}
