package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/outputs"
)

// markerRe matches the digest markers secretToken generates. Redaction
// passes treat markers as opaque, so a value that only matches inside a
// marker digest is neither replaced nor counted again.
var markerRe = regexp.MustCompile(`<gohan:secret:[0-9a-f]{24}>`)

// outsideMarkers applies fn to the parts of s outside generated markers
// and reassembles the markers verbatim.
func outsideMarkers(s string, fn func(string) string) string {
	locs := markerRe.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return fn(s)
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		b.WriteString(fn(s[last:loc[0]]))
		b.WriteString(s[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(fn(s[last:]))
	return b.String()
}

// secretPairs renders value→marker pairs, skipping empty values, so an
// empty resolution injects nothing and redacts nothing.
func secretPairs(secrets map[string]string) []string {
	pairs := make([]string, 0, 2*len(secrets))
	for _, v := range secrets {
		if v != "" {
			pairs = append(pairs, v, secretToken(v))
		}
	}
	return pairs
}

// sanitizeText applies the boundary secret pass to one text fragment, so
// an error message recorded in a result carries no resolved value.
func sanitizeText(secrets map[string]string, s string) string {
	if len(secrets) == 0 {
		return s
	}
	pairs := secretPairs(secrets)
	if len(pairs) == 0 {
		return s
	}
	repl := strings.NewReplacer(pairs...)
	clean, _ := redactText(repl, secrets, s)
	return clean
}

// boundarySanitizer builds the spiller's sanitize step from the resolved
// values: it replaces every occurrence outside generated markers with the
// value's digest token and returns the occurrence count once. It emits no
// telemetry itself and alters nothing when the policy injected no value,
// so a second pass over already-sanitized bytes reports zero.
func boundarySanitizer(secrets map[string]string) outputs.SanitizeFunc {
	return func(_ context.Context, _ string, data []byte) ([]byte, int, error) {
		if len(secrets) == 0 {
			return data, 0, nil
		}
		pairs := secretPairs(secrets)
		if len(pairs) == 0 {
			return data, 0, nil
		}
		repl := strings.NewReplacer(pairs...)
		clean, leaks := redactText(repl, secrets, string(data))
		return []byte(clean), leaks, nil
	}
}

// redactText replaces each resolved value in the non-marker parts of s and
// counts the occurrences it replaced. Generated markers stay untouched, so
// the count and the replacement agree and the pass is idempotent.
func redactText(repl *strings.Replacer, secrets map[string]string, s string) (string, int) {
	var n int
	out := outsideMarkers(s, func(seg string) string {
		for _, v := range secrets {
			if v != "" {
				n += strings.Count(seg, v)
			}
		}
		return repl.Replace(seg)
	})
	return out, n
}

// errNoSecretResolver marks a policy that names secrets while the build
// wired no resolver: nothing may be injected, and the call must fail rather
// than run the tool against an empty environment.
var errNoSecretResolver = errors.New("policy names secrets but no secret resolver is wired")

// resolveSecrets resolves the policy's named secrets for the run originator
// and injects them through SetEnv, the workspace's only environment path.
// It returns the resolved values so result redaction can scan for them; an
// empty map injects nothing.
func resolveSecrets(ctx context.Context, w types.Workspace, p types.SandboxPolicy) (map[string]string, error) {
	if len(p.Secrets) == 0 {
		return nil, nil
	}
	r, ok := types.SecretsFrom(ctx)
	if !ok {
		return nil, errNoSecretResolver
	}
	principal, _ := types.PrincipalFrom(ctx)
	env := make(map[string]string, len(p.Secrets))
	for _, ref := range p.Secrets {
		v, err := r.Resolve(ctx, principal, ref.Name)
		if err != nil {
			return nil, fmt.Errorf("resolve secret: %w", err)
		}
		env[ref.Env] = v
	}
	if err := w.SetEnv(ctx, env); err != nil {
		return nil, fmt.Errorf("set env: %w", err)
	}
	return env, nil
}

// secretToken renders the redaction marker for one resolved value in the
// shipped <gohan:kind:hex> shape, from a truncated digest, so the marker
// carries nothing of the value.
func secretToken(v string) string {
	sum := sha256.Sum256([]byte(v))
	return "<gohan:secret:" + hex.EncodeToString(sum[:12]) + ">"
}

// redactSecrets is the post-body safety pass over the tool's returned
// blocks and error text: it replaces every injected value with its marker
// and counts only the occurrences it actually replaces on the leak
// counter. Generated markers are opaque, so a second pass over already
// redacted content replaces nothing and counts nothing, keeping each
// occurrence counted exactly once across the boundary and safety passes.
func redactSecrets(ctx context.Context, secrets map[string]string, blocks []types.Block, err error) ([]types.Block, error) {
	if len(secrets) == 0 {
		return blocks, err
	}
	pairs := secretPairs(secrets)
	if len(pairs) == 0 {
		return blocks, err
	}
	repl := strings.NewReplacer(pairs...)
	var hits int64
	for i, b := range blocks {
		if t, ok := b.(types.Text); ok {
			clean, n := redactText(repl, secrets, t.Text)
			hits += int64(n)
			if n > 0 {
				blocks[i] = types.Text{BlockBase: t.BlockBase, Text: clean}
			}
		}
	}
	if err != nil {
		clean, n := redactText(repl, secrets, err.Error())
		hits += int64(n)
		if n > 0 {
			err = errors.New(clean)
		}
	}
	if hits == 0 {
		return blocks, err
	}
	if tel := types.TelemetryFrom(ctx); tel != nil {
		tel.Count(ctx, "gohan.sandbox.secret_leak", hits)
	}
	return blocks, err
}
