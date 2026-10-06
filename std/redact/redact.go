package redact

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/victorzhuk/gohan/core/types"
)

var (
	ErrModeUnsupported = errors.New("redaction token mode unsupported")
	ErrTokenCollision  = errors.New("redaction token collision")
	ErrMissingIdentity = errors.New("redaction tenant or session missing")
)

type Counter interface {
	Add(ctx context.Context, name string, value int64)
}

type Option func(*Redactor)

type Redactor struct {
	keys    types.KeySource
	counter Counter
}

func WithCounter(counter Counter) Option { return func(r *Redactor) { r.counter = counter } }

func New(keys types.KeySource, opts ...Option) *Redactor {
	r := &Redactor{keys: keys}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

var _ types.Redactor = (*Redactor)(nil)

type rule struct {
	kind     types.EntityKind
	pattern  *regexp.Regexp
	checksum string
}

var defaultRules = []rule{
	{kind: types.EntityEmail, pattern: regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`), checksum: "b10f8c82ac1fdf824efdaf0a60f8c8aaec395bb04a76d5c55f30aa4682d689c4"},
	{kind: types.EntityPhone, pattern: regexp.MustCompile(`\+?[0-9][0-9 ().-]{7,}[0-9]`), checksum: "ba3a2e7556b2cee787bb22deae29a232fb230fa3d6b1fd2793cb04400271fe08"},
	{kind: types.EntityCard, pattern: regexp.MustCompile(`(?:[0-9][ -]?){13,19}`), checksum: "7d6e2f4980087de62ef364816358c116830519f09964e55f6eeaf8d8c3963491"},
	{kind: types.EntityIBAN, pattern: regexp.MustCompile(`[A-Z]{2}[0-9]{2}(?: ?[A-Z0-9]){11,30}`), checksum: "2ab571a87131d4e61ff26964f80a019d7cf66d36a8d7b9303f5c794ca0d91e38"},
	{kind: types.EntityCustom, pattern: regexp.MustCompile(`\b[0-9]{3}-[0-9]{2}-[0-9]{4}\b`), checksum: "7625dd887084213e2f0456f8f26082c52a038be6b5b7c2e12afd5e1e5a459047"},
}

func ValidateRules() error {
	for _, rule := range defaultRules {
		sum := sha256.Sum256([]byte(rule.pattern.String()))
		if hex.EncodeToString(sum[:]) != rule.checksum {
			return fmt.Errorf("redaction rule checksum mismatch for %s: got %x", rule.kind, sum)
		}
	}
	return nil
}

func (r *Redactor) Redact(ctx context.Context, blocks []types.Block, policy types.RedactionPolicy) ([]types.Block, types.RedactionMap, error) {
	if policy.Mode != types.HMACToken {
		return nil, types.RedactionMap{}, ErrModeUnsupported
	}
	info, ok := types.RunInfoFrom(ctx)
	tenant := info.Principal.Tenant
	if !ok || tenant == "" || info.SessionID == "" {
		return nil, types.RedactionMap{}, ErrMissingIdentity
	}
	if r.keys == nil {
		return nil, types.RedactionMap{}, errors.New("redaction key source missing")
	}
	key, err := r.keys.Key(ctx, tenant)
	if err != nil {
		return nil, types.RedactionMap{}, fmt.Errorf("get redaction key: %w", err)
	}
	if len(key) == 0 {
		return nil, types.RedactionMap{}, errors.New("redaction key is empty")
	}
	if err := ValidateRules(); err != nil {
		return nil, types.RedactionMap{}, err
	}
	detectors := policy.Detectors
	if len(detectors) == 0 {
		detectors = []types.Detector{defaultDetector{}}
	}
	var spans []types.Span
	for _, detector := range detectors {
		detected, err := detector.Detect(ctx, blocks)
		if err != nil {
			return nil, types.RedactionMap{}, fmt.Errorf("detect entities: %w", err)
		}
		spans = append(spans, detected...)
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].Block != spans[j].Block {
			return spans[i].Block > spans[j].Block
		}
		return spans[i].Start > spans[j].Start
	})
	entries := make(map[string]types.Entity)
	out := append([]types.Block(nil), blocks...)
	lastBlock, lastStart := -1, int(^uint(0)>>1)
	for _, span := range spans {
		if span.Block < 0 || span.Block >= len(out) || span.Start < 0 || span.End <= span.Start {
			return nil, types.RedactionMap{}, errors.New("invalid redaction span")
		}
		text, ok := out[span.Block].(types.Text)
		original, originalOK := blocks[span.Block].(types.Text)
		if !ok || !originalOK || span.End > len(original.Text) {
			return nil, types.RedactionMap{}, errors.New("redaction span is outside text block")
		}
		if span.Block == lastBlock && span.End > lastStart {
			continue
		}
		lastBlock, lastStart = span.Block, span.Start
		if containsKind(policy.Preserve, span.Kind) {
			continue
		}
		value := original.Text[span.Start:span.End]
		canonicalValue := canonical(span.Kind, value)
		token := tokenFor(key, info.SessionID, span.Kind, canonicalValue)
		entity := types.Entity{Kind: span.Kind, Value: value}
		if old, exists := entries[token]; exists && (old.Kind != entity.Kind || canonical(old.Kind, old.Value) != canonicalValue) {
			return nil, types.RedactionMap{}, ErrTokenCollision
		}
		if _, exists := entries[token]; !exists {
			entries[token] = entity
		}
		text.Text = text.Text[:span.Start] + token + text.Text[span.End:]
		out[span.Block] = text
	}
	return out, types.RedactionMap{Tenant: tenant, Session: info.SessionID, Entries: entries}, nil
}

func (r *Redactor) Rehydrate(ctx context.Context, blocks []types.Block, m types.RedactionMap) ([]types.Block, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, ok := types.RunInfoFrom(ctx)
	tenant := info.Principal.Tenant
	if !ok || tenant == "" || info.SessionID == "" || m.Tenant != tenant || m.Session != info.SessionID {
		return nil, ErrMissingIdentity
	}
	out := append([]types.Block(nil), blocks...)
	for i, block := range out {
		text, ok := block.(types.Text)
		if !ok {
			continue
		}
		for token, entity := range m.Entries {
			text.Text = strings.ReplaceAll(text.Text, token, entity.Value)
		}
		out[i] = text
	}
	return out, nil
}

func tokenFor(key []byte, session string, kind types.EntityKind, value string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = fmt.Fprintf(mac, "%s\x00%s\x00%s", session, kind, value)
	return "<gohan:" + string(kind) + ":" + hex.EncodeToString(mac.Sum(nil)[:12]) + ">"
}

func canonical(kind types.EntityKind, value string) string {
	if kind == types.EntityPhone || kind == types.EntityCard {
		return strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, value)
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func containsKind(kinds []types.EntityKind, kind types.EntityKind) bool {
	for _, candidate := range kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

type defaultDetector struct{}

func (defaultDetector) Detect(ctx context.Context, blocks []types.Block) ([]types.Span, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var spans []types.Span
	for index, block := range blocks {
		text, ok := block.(types.Text)
		if !ok {
			continue
		}
		for _, rule := range defaultRules {
			for _, loc := range rule.pattern.FindAllStringIndex(text.Text, -1) {
				value := text.Text[loc[0]:loc[1]]
				if !validCandidate(rule.kind, value) {
					continue
				}
				spans = append(spans, types.Span{Block: index, Start: loc[0], End: loc[1], Kind: rule.kind})
			}
		}
	}
	return spans, nil
}

func validCandidate(kind types.EntityKind, value string) bool {
	switch kind {
	case types.EntityCard:
		return luhn(value)
	case types.EntityIBAN:
		return iban(value)
	default:
		return true
	}
}

func luhn(value string) bool {
	digits := canonical(types.EntityCard, value)
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum, double := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		n := int(digits[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}

func iban(value string) bool {
	compact := strings.ToUpper(strings.ReplaceAll(value, " ", ""))
	if len(compact) < 15 || len(compact) > 34 {
		return false
	}
	rotated := compact[4:] + compact[:4]
	rem := 0
	for _, r := range rotated {
		if r >= '0' && r <= '9' {
			rem = (rem*10 + int(r-'0')) % 97
			continue
		}
		if r < 'A' || r > 'Z' {
			return false
		}
		n := int(r-'A') + 10
		rem = (rem*10 + n/10) % 97
		rem = (rem*10 + n%10) % 97
	}
	return rem == 1
}

func UnknownToken(counter Counter, ctx context.Context) {
	if counter != nil {
		counter.Add(ctx, "gohan.redact.unknown_token", 1)
	}
}
