package std

import (
	"bytes"
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
// SHA-256 of the tool name and the exact canonical JSON of its arguments,
// prefixed with the exact-v1 version so a legacy rounded fingerprint can
// never match a fresh one. The canonical form sorts keys and renders every
// number by its exact decimal value, so key order, whitespace and decimal
// spelling do not change the fingerprint. The encoding is frozen: changing
// it breaks every replay window that spans the change.
func CanonicalFingerprint(tool string, args json.RawMessage) stores.Fingerprint {
	canonical, err := types.CanonicalJSONValue(args)
	if err != nil {
		canonical = args
	}
	sum := sha256.Sum256([]byte(tool + "\n" + string(canonical)))
	return stores.Fingerprint(types.ExactFingerprintVersion + hex.EncodeToString(sum[:]))
}

// legacyFingerprint reproduces the pre-exact journal fingerprint byte for
// byte: the SHA-256 of the tool name and the arguments re-encoded through
// float64, which rounds numbers above 2^53 onto one value. It only finds
// entries stored before the exact-v1 format; fresh reservations never
// store it.
func legacyFingerprint(tool string, args json.RawMessage) stores.Fingerprint {
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		return legacySum(tool, string(args))
	}
	b, err := json.Marshal(v)
	if err != nil {
		return legacySum(tool, string(args))
	}
	return legacySum(tool, string(b))
}

func legacySum(tool, canonical string) stores.Fingerprint {
	sum := sha256.Sum256([]byte(tool + "\n" + canonical))
	return stores.Fingerprint(hex.EncodeToString(sum[:]))
}

// proveLegacyIntent refuses a legacy candidate whose stored evidence cannot
// prove the new call: identical raw bytes, or equal canonical forms once
// numbers render by their exact decimal value. Missing or unreadable
// evidence refuses, as does any surviving inequality.
func proveLegacyIntent(stored, args json.RawMessage) error {
	if len(stored) == 0 {
		return fmt.Errorf("stored arguments missing: %w", stores.ErrJournalFingerprintMismatch)
	}
	if bytes.Equal(stored, args) {
		return nil
	}
	storedCanon, err := types.CanonicalJSONValue(stored)
	if err != nil {
		return fmt.Errorf("stored arguments unreadable: %w", stores.ErrJournalFingerprintMismatch)
	}
	argsCanon, err := types.CanonicalJSONValue(args)
	if err != nil {
		return fmt.Errorf("call arguments unreadable: %w", stores.ErrJournalFingerprintMismatch)
	}
	if !bytes.Equal(storedCanon, argsCanon) {
		return fmt.Errorf("stored arguments differ: %w", stores.ErrJournalFingerprintMismatch)
	}
	return nil
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
			args := json.RawMessage(call.Args)
			fp := CanonicalFingerprint(call.Name, args)
			pinned := false
			if prior, err := j.ByFingerprint(ctx, session, fp); err == nil {
				for _, e := range prior {
					if e.State == stores.Reserved || e.Result.Outcome == types.Unknown {
						key.CallID = e.Key
						pinned = true
						break
					}
				}
			}
			if !pinned {
				// A legacy fingerprint is only an unresolved-intent
				// candidate pointer: rounding can collide unequal
				// arguments, so the stored entry proves nothing until its
				// complete arguments match the call exactly.
				if prior, err := j.ByFingerprint(ctx, session, legacyFingerprint(call.Name, args)); err == nil {
					for _, e := range prior {
						if e.State != stores.Reserved && e.Result.Outcome != types.Unknown {
							continue
						}
						if err := proveLegacyIntent(e.Args, args); err != nil {
							// The refused intent must not run the effect.
							return types.ToolResult{}, fmt.Errorf("journal intent %s: %w", call.Name, err)
						}
						key.CallID = e.Key
						fp = e.Fingerprint
						break
					}
				}
			}
			entry, created, err := j.Reserve(ctx, key, call.Name, fp, args)
			if err != nil {
				// A refused identity must not run the effect: the tool
				// would execute under a call key the journal declined.
				return types.ToolResult{}, fmt.Errorf("reserve %s: %w", call.Name, err)
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
			}
			// The recorded result carries the pinned entry identity, not
			// the caller's: reconciliation resolves the entry by the
			// recorded ID, and a retried call completes under the key it
			// inherited.
			res.ID = entry.Key
			// The effect already ran, so a lost completion leaves
			// uncertainty, not a failed call that invites a retry.
			if err := j.Complete(ctx, key, res); err != nil {
				res.Outcome = types.Unknown
			}
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
