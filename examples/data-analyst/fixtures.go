package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"path/filepath"
	"strings"
	"sync"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/redact"
)

const (
	// secretNameWarehouse is the only secret name the example resolves;
	// its value reaches the workspace only through Workspace.SetEnv.
	secretNameWarehouse = "warehouse"
	// fixtureSecretValue is deliberately not credential-shaped, so the
	// fixture can never leak a usable secret.
	fixtureSecretValue = "not-a-real-credential"
)

var (
	errPathOutsideRoot = errors.New("path escapes workspace root")
	errFileNotFound    = errors.New("no such file")
	errUnknownSecret   = errors.New("unknown secret")
)

// fakeCounters tallies the sandbox port calls a scenario exercised.
type fakeCounters struct {
	Opens, Execs, Puts, Gets, SetEnvs, Snapshots, Closes int
}

// fakeSandbox is the offline sandbox: files live in a map keyed by
// snapshot ref, so a closed workspace's snapshot still restores on a
// later Open. Every path is confined to root before it is stored; root
// is a boundary for path checks only and nothing touches the filesystem.
type fakeSandbox struct {
	mu          sync.Mutex
	root        string
	seed        map[string][]byte
	backend     map[types.WorkspaceRef]map[string][]byte
	snapSeq     int
	execScript  []types.ExecResult
	tally       fakeCounters
	envCalls    []map[string]string
	execReqs    []types.ExecRequest
	execResults []types.ExecResult
	snapRefs    []types.WorkspaceRef
	last        *fakeWorkspace
}

var _ types.Sandbox = (*fakeSandbox)(nil)

// newFakeSandbox builds the sandbox double. The caller passes a scratch
// root (t.TempDir() in tests) that bounds the confined path space.
func newFakeSandbox(root string) *fakeSandbox {
	return &fakeSandbox{
		root:    root,
		seed:    make(map[string][]byte),
		backend: make(map[types.WorkspaceRef]map[string][]byte),
	}
}

// scriptExec programs the exec results every workspace opened afterwards
// walks through; the last result repeats once the script runs out.
func (s *fakeSandbox) scriptExec(results ...types.ExecResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.execScript = make([]types.ExecResult, len(results))
	for i, res := range results {
		s.execScript[i] = copyResult(res)
	}
}

func (s *fakeSandbox) Open(_ context.Context, ref types.WorkspaceRef, _ types.SandboxPolicy) (types.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tally.Opens++
	files := copyFileMap(s.seed)
	if ref != "" {
		if stored, ok := s.backend[ref]; ok {
			files = copyFileMap(stored)
		}
	}
	w := &fakeWorkspace{s: s, files: files}
	s.last = w
	return w, nil
}

// counters reports the port-call tally as a value copy.
func (s *fakeSandbox) counters() fakeCounters {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tally
}

// setEnvCalls reports every recorded env map, deep-copied.
func (s *fakeSandbox) setEnvCalls() []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]string, len(s.envCalls))
	for i, env := range s.envCalls {
		out[i] = copyEnvMap(env)
	}
	return out
}

// execCalls reports every recorded exec request, deep-copied.
func (s *fakeSandbox) execCalls() []types.ExecRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]types.ExecRequest, len(s.execReqs))
	for i, req := range s.execReqs {
		out[i] = copyExecRequest(req)
	}
	return out
}

// lastExec reports the most recent exec result, deep-copied, so a test
// can assert on the stdout a tool consumed.
func (s *fakeSandbox) lastExec() types.ExecResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.execResults) == 0 {
		return types.ExecResult{}
	}
	return copyResult(s.execResults[len(s.execResults)-1])
}

// files reports the file map of the most recently opened workspace,
// deep-copied; an empty map means no workspace or no files yet.
func (s *fakeSandbox) files() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return map[string][]byte{}
	}
	return copyFileMap(s.last.files)
}

// snapshotRefs reports the refs Snapshot minted, in mint order.
func (s *fakeSandbox) snapshotRefs() []types.WorkspaceRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]types.WorkspaceRef(nil), s.snapRefs...)
}

// confine resolves a workspace path or rejects it. Absolute paths, any
// ".." element and anything that would land outside root are refused
// before any byte is written; the returned key stays workspace-relative.
func (s *fakeSandbox) confine(path string) (string, error) {
	if !filepath.IsLocal(path) {
		return "", fmt.Errorf("workspace path %q: %w", path, errPathOutsideRoot)
	}
	clean := filepath.Clean(path)
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == ".." {
			return "", fmt.Errorf("workspace path %q: %w", path, errPathOutsideRoot)
		}
	}
	joined := filepath.Join(s.root, clean)
	if rel, err := filepath.Rel(s.root, joined); err != nil ||
		rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace path %q: %w", path, errPathOutsideRoot)
	}
	return clean, nil
}

// fakeAdapter is an independent adapter value over the shared fake
// backend: Open delegates, so the composed run gets a second stack with
// its own coordinator while a resumed turn still restores the snapshot
// and the extract the first stack stored.
type fakeAdapter struct {
	sb *fakeSandbox
}

var _ types.Sandbox = fakeAdapter{}

func (a fakeAdapter) Open(ctx context.Context, ref types.WorkspaceRef, p types.SandboxPolicy) (types.Workspace, error) {
	return a.sb.Open(ctx, ref, p)
}

// newAdapter returns a fresh adapter value over the same fake; backend
// map, exec script and counters stay shared across instances.
func (s *fakeSandbox) newAdapter() types.Sandbox {
	return fakeAdapter{sb: s}
}

// fakeWorkspace is the workspace one Open handed out. It carries its own
// exec cursor, so two workspaces from the same script replay it
// independently.
type fakeWorkspace struct {
	s      *fakeSandbox
	files  map[string][]byte
	cursor int
}

var _ types.Workspace = (*fakeWorkspace)(nil)

func (w *fakeWorkspace) Exec(_ context.Context, req types.ExecRequest) (types.ExecResult, error) {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	w.s.tally.Execs++
	w.s.execReqs = append(w.s.execReqs, copyExecRequest(req))
	res := w.nextResultLocked()
	w.s.execResults = append(w.s.execResults, copyResult(res))
	return res, nil
}

func (w *fakeWorkspace) nextResultLocked() types.ExecResult {
	script := w.s.execScript
	if len(script) == 0 {
		return types.ExecResult{}
	}
	if w.cursor < len(script) {
		res := script[w.cursor]
		w.cursor++
		return res
	}
	return script[len(script)-1]
}

func (w *fakeWorkspace) Put(_ context.Context, path string, r io.Reader) error {
	clean, err := w.s.confine(path)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	w.s.tally.Puts++
	w.files[clean] = data
	return nil
}

func (w *fakeWorkspace) Get(_ context.Context, path string) (io.ReadCloser, error) {
	clean, err := w.s.confine(path)
	if err != nil {
		return nil, err
	}
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	w.s.tally.Gets++
	data, ok := w.files[clean]
	if !ok {
		return nil, fmt.Errorf("read %q: %w", path, errFileNotFound)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (w *fakeWorkspace) SetEnv(_ context.Context, env map[string]string) error {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	w.s.tally.SetEnvs++
	w.s.envCalls = append(w.s.envCalls, copyEnvMap(env))
	return nil
}

func (w *fakeWorkspace) Snapshot(_ context.Context) (types.WorkspaceRef, error) {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	return w.snapshotLocked()
}

func (w *fakeWorkspace) snapshotLocked() (types.WorkspaceRef, error) {
	w.s.snapSeq++
	ref := types.WorkspaceRef(fmt.Sprintf("ws-%d", w.s.snapSeq))
	w.s.backend[ref] = copyFileMap(w.files)
	w.s.tally.Snapshots++
	w.s.snapRefs = append(w.s.snapRefs, ref)
	return ref, nil
}

func (w *fakeWorkspace) Close(_ context.Context) error {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	w.s.tally.Closes++
	return nil
}

// fakeSecretResolver answers exactly one fixed secret name with one
// fixed offline value and refuses everything else.
type fakeSecretResolver struct {
	value string
}

var _ types.SecretResolver = (*fakeSecretResolver)(nil)

func newFakeSecretResolver(value string) *fakeSecretResolver {
	return &fakeSecretResolver{value: value}
}

func (r *fakeSecretResolver) Resolve(_ context.Context, _ types.Principal, name string) (string, error) {
	if name != secretNameWarehouse {
		return "", fmt.Errorf("resolve secret %q: %w", name, errUnknownSecret)
	}
	return r.value, nil
}

// scriptedModel replays fixed chunk sequences keyed by turn number and
// stops the turn deterministically when no script covers it. It is safe
// for one sequential run, which is all the demo needs.
type scriptedModel struct {
	turns map[int][]types.ModelChunk
	i     int
}

var _ types.Model = (*scriptedModel)(nil)

func newScriptedModel(turns map[int][]types.ModelChunk) *scriptedModel {
	return &scriptedModel{turns: turns}
}

func (m *scriptedModel) Profile() types.ModelProfile { return types.ModelProfile{Name: "scripted"} }

func (m *scriptedModel) Generate(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		chunks, ok := m.turns[m.i]
		if !ok {
			chunks = []types.ModelChunk{{Finish: types.FinishStop}}
		}
		for _, c := range chunks {
			if !yield(c, nil) {
				return
			}
		}
		m.i++
	}
}

func copyFileMap(src map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(src))
	for path, data := range src {
		out[path] = append([]byte(nil), data...)
	}
	return out
}

func copyEnvMap(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}

func copyExecRequest(req types.ExecRequest) types.ExecRequest {
	out := req
	out.Argv = append([]string(nil), req.Argv...)
	out.Stdin = append([]byte(nil), req.Stdin...)
	return out
}

func copyResult(res types.ExecResult) types.ExecResult {
	out := res
	out.Stdout = append([]byte(nil), res.Stdout...)
	out.Stderr = append([]byte(nil), res.Stderr...)
	return out
}

// fixedKeySource answers every tenant with the same offline phrase, so
// HMAC tokens stay deterministic across a demo run and never look like
// a credential.
type fixedKeySource struct{}

func (fixedKeySource) Key(_ context.Context, _ string) ([]byte, error) {
	return []byte("data-analyst example offline redaction key"), nil
}

// newRedactor builds the redactor behind both output guards: the chain
// step that masks tool results before the scheduler journals them and
// the store wrapper that masks content before it is stored.
func newRedactor() *redact.Redactor {
	return redact.New(fixedKeySource{})
}

// newGuardChain masks personally identifying rows in a tool result
// before it is journaled, so no clear identity survives into history.
func newGuardChain(red *redact.Redactor) chains.ToolChain {
	return chains.ToolChain{
		{
			Name: "redact-output",
			Kind: chains.KindRedact,
			Use: func(next types.ToolFunc) types.ToolFunc {
				return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
					res, err := next(ctx, call)
					if err != nil {
						return res, err
					}
					if len(res.Content) == 0 {
						return res, nil
					}
					masked, _, err := red.Redact(ctx, res.Content, types.RedactionPolicy{Mode: types.HMACToken})
					if err != nil {
						return res, fmt.Errorf("redact tool output: %w", err)
					}
					res.Content = masked
					return res, nil
				}
			},
		},
	}
}

// redactingOutputs masks identifying rows before the inner store sees
// the content, so the stored bytes and the ref they hash to never carry
// clear identity; the inner store keeps owning ref semantics.
type redactingOutputs struct {
	inner stores.OutputStore
	red   *redact.Redactor
}

var _ stores.OutputStore = (*redactingOutputs)(nil)

func newRedactingOutputs(inner stores.OutputStore) *redactingOutputs {
	return &redactingOutputs{inner: inner, red: newRedactor()}
}

func (s *redactingOutputs) Put(ctx context.Context, ri types.RunInfo, content []types.Block) (string, error) {
	masked, _, err := s.red.Redact(types.WithRunInfo(ctx, ri), content, types.RedactionPolicy{Mode: types.HMACToken})
	if err != nil {
		return "", fmt.Errorf("redact output: %w", err)
	}
	return s.inner.Put(ctx, ri, masked)
}

func (s *redactingOutputs) Get(ctx context.Context, ref string) ([]byte, error) {
	return s.inner.Get(ctx, ref)
}
