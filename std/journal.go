package std

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// SpecLookup resolves a tool name to its spec. Steps use it to read the
// effect and read-back declaration of the call they wrap.
type SpecLookup func(name string) (types.ToolSpec, bool)

// CanonicalFingerprint computes the journal fingerprint for a call: the
// SHA-256 of the tool name and the canonical JSON of its arguments. The
// canonical form re-encodes the arguments so key order and whitespace do
// not change the fingerprint. The encoding is frozen: changing it breaks
// every replay window that spans the change.
func CanonicalFingerprint(tool string, args json.RawMessage) stores.Fingerprint {
	sum := sha256.Sum256([]byte(tool + "\n" + canonicalJSON(args)))
	return stores.Fingerprint(hex.EncodeToString(sum[:]))
}

func canonicalJSON(args json.RawMessage) string {
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		return string(args)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(args)
	}
	return string(b)
}

// JournalOption configures Journal.
type JournalOption func(*journalConfig)

type journalConfig struct {
	specs     SpecLookup
	prompts   chains.PromptSet
	sessionID func(context.Context) string
}

// WithJournalSpecs sets the tool spec lookup. Tools whose spec is ReadOnly
// skip the journal entirely.
func WithJournalSpecs(lookup SpecLookup) JournalOption {
	return func(c *journalConfig) { c.specs = lookup }
}

// WithJournalPrompts sets the prompt strings used to render the read-back
// hint on an unknown outcome.
func WithJournalPrompts(p chains.PromptSet) JournalOption {
	return func(c *journalConfig) { c.prompts = p }
}

// WithJournalSessionID overrides how the session id is read from ctx. The
// default reads RunInfoFrom.
func WithJournalSessionID(fn func(context.Context) string) JournalOption {
	return func(c *journalConfig) { c.sessionID = fn }
}

// Journal returns the journal middleware.
func Journal(j stores.Journal, opts ...JournalOption) chains.ToolMiddleware {
	cfg := journalConfig{
		prompts: DefaultPrompts,
		sessionID: func(ctx context.Context) string {
			info, ok := gohan.RunInfoFrom(ctx)
			if !ok {
				return ""
			}
			return info.SessionID
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(next chains.ToolFunc) chains.ToolFunc {
		return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			effect := types.SideEffect
			var spec types.ToolSpec
			if cfg.specs != nil {
				if s, ok := cfg.specs(call.Name); ok {
					spec = s
					effect = s.Effect
				}
			}
			if effect == types.ReadOnly {
				return next(ctx, call)
			}
			session := cfg.sessionID(ctx)
			key := types.CallKey{SessionID: session, CallID: call.ID}
			fp := CanonicalFingerprint(call.Name, json.RawMessage(call.Args))
			if prior, err := j.ByFingerprint(ctx, session, fp); err == nil {
				for _, e := range prior {
					if e.State == stores.Reserved || e.Result.Outcome == types.Unknown {
						key.CallID = e.Key
						break
					}
				}
			}
			entry, created, err := j.Reserve(ctx, key, fp)
			if err != nil {
				return next(ctx, call)
			}
			if !created && entry.State == stores.Completed && entry.Result.Outcome != types.Unknown {
				return entry.Result, nil
			}
			if !created {
				ctx = gohan.WithIdempotencyKey(ctx, entry.Key)
			}
			res, err := next(ctx, call)
			if err != nil {
				return res, err
			}
			if res.Outcome == types.Unknown {
				if spec.ReadBack != "" {
					res = ReadBackResult(spec, cfg.prompts, res)
				}
				_ = j.Complete(ctx, key, res)
				return res, nil
			}
			_ = j.Complete(ctx, key, res)
			return res, nil
		}
	}
}

// ReadBackResult renders the model-visible result for an unknown outcome:
// the prompt sentence plus the name of the read-back tool, so the model can
// verify the effect instead of retrying it.
func ReadBackResult(spec types.ToolSpec, prompts chains.PromptSet, r types.ToolResult) types.ToolResult {
	text := fmt.Sprintf("%s Read back the effect with %s.", prompts.OutcomeUnknown, spec.ReadBack)
	r.Content = append(r.Content, types.Text{Text: text})
	return r
}
