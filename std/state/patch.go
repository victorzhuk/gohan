package state

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/victorzhuk/gohan/core/types"
)

// Patch returns the RFC 6902 operations that transform prev into next.
// Both values go through JSON encoding, so anything not JSON-serialisable
// is an error. Object members diff key by key with JSON Pointer escaping;
// arrays that differ are replaced whole, which keeps the op set minimal to
// specify and order-independent for the client to apply.
func Patch(prev, next any) ([]types.PatchOp, error) {
	a, err := decode(prev)
	if err != nil {
		return nil, fmt.Errorf("state: previous value: %w", err)
	}
	b, err := decode(next)
	if err != nil {
		return nil, fmt.Errorf("state: next value: %w", err)
	}
	return diff("", a, b), nil
}

func decode(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// diff appends the operations for path, where a is the current document
// and b the target. Both sides are decoded JSON values.
func diff(path string, a, b any) []types.PatchOp {
	if equal(a, b) {
		return nil
	}
	am, _ := a.(map[string]any)
	bm, _ := b.(map[string]any)
	if am != nil || bm != nil {
		// A nil previous is the empty object: the first update adds its
		// members instead of replacing the whole document.
		return diffObjects(path, am, bm)
	}
	// Mismatched shapes and scalars replace at the path; RFC 6902 allows
	// replace of the whole document at the empty path.
	return []types.PatchOp{{Op: "replace", Path: path, Value: b}}
}

func diffObjects(path string, a, b map[string]any) []types.PatchOp {
	var ops []types.PatchOp
	removed := make([]string, 0, len(a))
	for k := range a {
		if _, ok := b[k]; !ok {
			removed = append(removed, k)
		}
	}
	sort.Strings(removed)
	for _, k := range removed {
		ops = append(ops, types.PatchOp{Op: "remove", Path: child(path, k)})
	}
	added := make([]string, 0, len(b))
	for k := range b {
		if _, ok := a[k]; !ok {
			added = append(added, k)
		}
	}
	sort.Strings(added)
	for _, k := range added {
		ops = append(ops, types.PatchOp{Op: "add", Path: child(path, k), Value: b[k]})
	}
	shared := make([]string, 0, len(b))
	for k := range b {
		if _, ok := a[k]; ok {
			shared = append(shared, k)
		}
	}
	sort.Strings(shared)
	for _, k := range shared {
		ops = append(ops, diff(child(path, k), a[k], b[k])...)
	}
	return ops
}

func child(path, key string) string {
	key = strings.ReplaceAll(key, "~", "~0")
	key = strings.ReplaceAll(key, "/", "~1")
	if path == "" {
		return "/" + key
	}
	return path + "/" + key
}

func equal(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			w, ok := bv[k]
			if !ok || !equal(v, w) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i, v := range av {
			if !equal(v, bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}
