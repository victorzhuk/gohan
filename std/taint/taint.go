package taint

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"strconv"
	"strings"

	"github.com/victorzhuk/gohan/core/types"
)

type TaintPolicy struct {
	ToExfil        types.TaintAction
	ToSideEffect   types.TaintAction
	ToReadOnly     types.TaintAction
	MinMatch       int
	MaxWindowBytes int
}

func Default() TaintPolicy {
	return TaintPolicy{
		ToExfil:        types.TaintDeny,
		ToSideEffect:   types.TaintAsk,
		ToReadOnly:     types.TaintAllow,
		MinMatch:       16,
		MaxWindowBytes: 262144,
	}
}

var ErrInvalidPolicy = errors.New("taint: invalid policy")

const defaultMaxWindowBytes = 262144

func New(policy TaintPolicy, tel types.Telemetry) (types.TaintHook, error) {
	for _, a := range []types.TaintAction{policy.ToExfil, policy.ToSideEffect, policy.ToReadOnly} {
		if a < types.TaintAllow || a > types.TaintDeny {
			return nil, ErrInvalidPolicy
		}
	}
	if policy.MinMatch < 1 {
		return nil, ErrInvalidPolicy
	}
	if policy.MaxWindowBytes < 0 {
		return nil, ErrInvalidPolicy
	}
	cap := policy.MaxWindowBytes
	if cap == 0 {
		cap = defaultMaxWindowBytes
	}
	return func(ctx context.Context, spec types.ToolSpec, args jsontext.Value) (types.TaintAction, []types.ArgTaint) {
		return evaluate(ctx, policy, cap, tel, spec, args)
	}, nil
}

func evaluate(ctx context.Context, policy TaintPolicy, cap int, tel types.Telemetry, spec types.ToolSpec, args jsontext.Value) (types.TaintAction, []types.ArgTaint) {
	blocks, ok := types.TaintWindowFrom(ctx)
	if !ok {
		return types.TaintAllow, nil
	}
	blocks = afterLastUser(blocks)
	blocks = truncateWindow(ctx, blocks, cap, tel)
	type segment struct {
		text   string
		origin types.Origin
	}
	var segments []segment
	for _, b := range blocks {
		o := b.BlockOrigin()
		if o.Kind == types.OriginSystem || o.Kind == types.OriginUser {
			continue
		}
		for _, t := range blockTexts(b) {
			segments = append(segments, segment{text: t, origin: o})
		}
	}
	var taints []types.ArgTaint
	for path, value := range argStrings(args) {
		for _, s := range segments {
			if len(value) < policy.MinMatch {
				if value != s.text {
					continue
				}
			} else if !strings.Contains(s.text, value) {
				continue
			}
			taints = append(taints, types.ArgTaint{Arg: path, Origins: []types.Origin{s.origin}, Match: value})
		}
	}
	if len(taints) == 0 {
		return types.TaintAllow, nil
	}
	action := policy.ToReadOnly
	if spec.Capabilities.Exfil {
		action = policy.ToExfil
	} else if spec.Effect == types.SideEffect || spec.Effect == types.Idempotent {
		action = policy.ToSideEffect
	}
	switch action {
	case types.TaintDeny:
		if tel != nil {
			tel.Count(ctx, "gohan.taint.denied", 1, types.String("tool", spec.Name))
		}
	case types.TaintAsk:
		if tel != nil {
			tel.Count(ctx, "gohan.taint.asked", 1, types.String("tool", spec.Name))
		}
	}
	return action, taints
}

func afterLastUser(blocks []types.Block) []types.Block {
	start := 0
	for i, b := range blocks {
		if b.BlockOrigin().Kind == types.OriginUser {
			start = i + 1
		}
	}
	return blocks[start:]
}

func truncateWindow(ctx context.Context, blocks []types.Block, cap int, tel types.Telemetry) []types.Block {
	total := 0
	for _, b := range blocks {
		total += textBytes(b)
	}
	if total <= cap {
		return blocks
	}
	start := 0
	for total > cap && start < len(blocks) {
		total -= textBytes(blocks[start])
		start++
	}
	if tel != nil {
		tel.Count(ctx, "gohan.taint.window_truncated", 1)
	}
	return blocks[start:]
}

func blockTexts(b types.Block) []string {
	switch b := b.(type) {
	case types.Text:
		return []string{b.Text}
	case types.Reasoning:
		return []string{b.Text}
	case types.ToolResult:
		return nestedTexts(b.Content)
	case types.Document:
		return nestedTexts(b.Content)
	}
	return nil
}

func nestedTexts(blocks []types.Block) []string {
	var out []string
	for _, b := range blocks {
		out = append(out, blockTexts(b)...)
	}
	return out
}

func textBytes(b types.Block) int {
	n := 0
	for _, t := range blockTexts(b) {
		n += len(t)
	}
	return n
}

func argStrings(args jsontext.Value) map[string]string {
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		return nil
	}
	out := make(map[string]string)
	walkArgs("", v, out)
	return out
}

func walkArgs(path string, v any, out map[string]string) {
	switch v := v.(type) {
	case string:
		out[path] = v
	case map[string]any:
		for k, child := range v {
			p := k
			if path != "" {
				p = path + "." + k
			}
			walkArgs(p, child, out)
		}
	case []any:
		for i, child := range v {
			walkArgs(path+"["+strconv.Itoa(i)+"]", child, out)
		}
	}
}
