package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/victorzhuk/gohan/core/types"
)

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

// redactSecrets replaces every injected value in the tool's returned blocks
// and error text with its marker and counts the hits on the leak counter.
// It runs before the result leaves the tool, so a leak never reaches
// history, journal or logs.
func redactSecrets(ctx context.Context, secrets map[string]string, blocks []types.Block, err error) ([]types.Block, error) {
	if len(secrets) == 0 {
		return blocks, err
	}
	pairs := make([]string, 0, 2*len(secrets))
	for _, v := range secrets {
		if v != "" {
			pairs = append(pairs, v, secretToken(v))
		}
	}
	if len(pairs) == 0 {
		return blocks, err
	}
	repl := strings.NewReplacer(pairs...)
	var hits int64
	for _, v := range secrets {
		if v == "" {
			continue
		}
		for _, b := range blocks {
			if t, ok := b.(types.Text); ok {
				hits += int64(strings.Count(t.Text, v))
			}
		}
		if err != nil {
			hits += int64(strings.Count(err.Error(), v))
		}
	}
	if hits == 0 {
		return blocks, err
	}
	for i, b := range blocks {
		if t, ok := b.(types.Text); ok {
			blocks[i] = types.Text{BlockBase: t.BlockBase, Text: repl.Replace(t.Text)}
		}
	}
	if err != nil {
		for _, v := range secrets {
			if v != "" && strings.Contains(err.Error(), v) {
				err = errors.New(repl.Replace(err.Error()))
				break
			}
		}
	}
	if tel := types.TelemetryFrom(ctx); tel != nil {
		tel.Count(ctx, "gohan.sandbox.secret_leak", hits)
	}
	return blocks, err
}
