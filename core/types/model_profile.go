package types

import (
	"strings"
	"time"
)

// ModelProfile pins one provider endpoint: the model version it was
// validated against, what it can do, what it costs and how it fails.
type ModelProfile struct {
	Name          string
	Version       string
	Sunset        time.Time
	Successor     string
	Region        string
	Endpoint      string
	QuotaPool     string
	Caps          Caps
	ContextWindow int
	Pricing       Pricing
	MaxInFlight   int
	Affinity      AffinityKeyStrategy
	LatencyClass  LatencyClass
	Timeout       ModelTimeout
	Keys          KeyMode
}

// KeyMode selects how a profile obtains its provider credential: from the
// platform account, from the caller's tenant, or from the tenant with a
// fallback to the platform account only when the tenant has no key.
type KeyMode int

const (
	PlatformKey KeyMode = iota
	TenantKey
	TenantOrPlatformKey
)

// ModelTimeout bounds a model stream. Connect and FirstChunk expiry is
// transient; an Idle expiry after the first chunk is permanent for the call.
type ModelTimeout struct {
	Connect    time.Duration
	FirstChunk time.Duration
	Idle       time.Duration
}

// Caps records what a profile can do. Build resolves flow options against
// it and rejects impossible combinations.
type Caps struct {
	Tools            bool
	ParallelTools    bool
	Constrained      bool
	Images           bool
	Streaming        bool
	Temperature      bool
	StrictVersion    bool
	Cache            CacheMode
	CacheBreakpoints int
	Compaction       CompactionMode
	ReasoningVisible bool
	Fidelity         map[BlockKind]Fidelity
	ProviderTools    map[string]ProviderToolCap
	Blobs            BlobCaps
	StreamsToolArgs  bool
}

// BlobCaps is the profile's blob policy: the hard per-block cap, the
// per-request count, the pixel cap and the accepted MIME formats.
type BlobCaps struct {
	MaxBytes      int64
	MaxPerRequest int
	MaxPixels     int
	Formats       []string
}

// Pricing is per token; SandboxSecond per sandbox second. SocializeCacheWrites
// folds cache-write cost into cached reads; BatchDiscount is applied pro rata
// per item by token share.
type Pricing struct {
	Input                float64
	CachedInput          float64
	CacheWrite           float64
	Output               float64
	BatchDiscount        float64
	SocializeCacheWrites bool
	SandboxSecond        float64
	ProviderCall         map[string]float64
}

// ProviderToolCap records what a provider-executed tool may do; Approval
// marks tools that need an approval verdict before the provider runs them.
type ProviderToolCap struct {
	Approval bool
}

// CacheMode selects the provider cache strategy: none, provider-side prefix
// caching, or explicit breakpoints mapped in order.
type CacheMode int

const (
	CacheNone CacheMode = iota
	CacheAuto
	CacheExplicit
)

// CompactionMode selects how a profile compacts history.
type CompactionMode int

const (
	CompactionNone CompactionMode = iota
	CompactionTextCap
	CompactionOpaqueCap
)

// Quota code substrings a provider uses for exhausted quota, matched
// case-insensitively before the status mapping.
var quotaCodes = []string{"insufficient_quota", "quota_exceeded", "rate_limit_exceeded"}

var contextOverflowCodes = []string{"context_length"}

var contentPolicyCodes = []string{"content_policy", "content_filter"}

var retiredModelCodes = []string{"model_not_found", "model_deprecated", "model_retired"}

// ClassifyProviderError maps a provider failure onto the adapter contract's
// error-class table: quota and 429 to ClassRateLimited, connection and 5xx to
// ClassTransient, context length to ClassContextOverflow, safety refusals to
// ClassContentPolicy, retired or unknown models to ClassDeprecated, 401/403
// to ClassAuth, and 400 or anything unrecognised to ClassPermanent. Version
// drift is not derivable from a status and code, so adapters construct
// ClassVersionDrift directly.
func ClassifyProviderError(status int, code string) ErrorClass {
	code = strings.ToLower(code)
	switch {
	case containsAny(quotaCodes, code):
		return ClassRateLimited
	case containsAny(contextOverflowCodes, code):
		return ClassContextOverflow
	case containsAny(contentPolicyCodes, code):
		return ClassContentPolicy
	case containsAny(retiredModelCodes, code):
		return ClassDeprecated
	case status == 429:
		return ClassRateLimited
	case status >= 500:
		return ClassTransient
	case status == 401 || status == 403:
		return ClassAuth
	default:
		return ClassPermanent
	}
}

func containsAny(list []string, code string) bool {
	for _, want := range list {
		if strings.Contains(code, want) {
			return true
		}
	}
	return false
}
