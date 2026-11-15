package mcp

import (
	"context"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
)

const manifestDriftMetric = "gohan.tool.manifest_drift"

// Set holds one imported MCP server: its session, its accepted catalog and
// the refresh loop that keeps the catalog in step with the server.
type Set struct {
	url             string
	ns              string
	session         *mcp.ClientSession
	cps             stores.Checkpoints
	policy          gohan.ToolPolicy
	pinned          map[string]string
	credentials     types.CredentialSource
	callTimeout     time.Duration
	telemetry       types.Telemetry
	refreshInterval time.Duration
	cacheScope      string

	mu       sync.Mutex
	tools    map[string]*importedTool
	disabled map[string]bool
	counted  map[string]bool
	started  bool
	loopWG   sync.WaitGroup
	done     chan struct{}
	closeErr error
	closeOne sync.Once
}

// Tools returns the current accepted catalog. The first call fetches it;
// later calls return the refreshed snapshot.
func (s *Set) Tools(ctx context.Context) ([]types.Tool, error) {
	s.mu.Lock()
	if !s.started {
		s.started = true
		if err := s.refresh(ctx); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		s.loopWG.Add(1)
		go s.loop()
	}
	out := make([]types.Tool, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, t)
	}
	s.mu.Unlock()
	return out, nil
}

// Close stops the refresh loop and the underlying session.
func (s *Set) Close() error {
	s.closeOne.Do(func() {
		close(s.done)
		s.loopWG.Wait()
		s.closeErr = s.session.Close()
	})
	return s.closeErr
}

func (s *Set) loop() {
	defer s.loopWG.Done()
	s.mu.Lock()
	interval := s.refreshInterval
	s.mu.Unlock()
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), interval)
			s.mu.Lock()
			_ = s.refresh(ctx)
			s.mu.Unlock()
			cancel()
		}
	}
}

// refresh fetches the catalog, applies the pin drift rule and rebuilds the
// accepted snapshot. The caller holds s.mu.
func (s *Set) refresh(ctx context.Context) error {
	res, err := s.session.ListTools(ctx, nil)
	if err != nil {
		return err
	}
	s.cacheScope = res.GetCacheScope()
	if s.refreshInterval == 0 {
		d := time.Duration(res.GetTTLMs()) * time.Millisecond
		if d < time.Second {
			d = time.Second
		}
		s.refreshInterval = d
	}

	specs := map[string]types.ToolSpec{}
	built := map[string]*importedTool{}
	for _, t := range res.Tools {
		tool, spec, err := s.buildTool(t)
		if err != nil {
			return err
		}
		specs[spec.Name] = spec
		built[spec.Name] = tool
	}

	if s.pinned == nil {
		s.pinned = std.ManifestOf(specList(specs))
	}
	source := "mcp"
	if s.ns != "" {
		source = "mcp:" + s.ns
	}
	for name := range s.pinned {
		if s.disabled[name] {
			continue
		}
		cur, ok := specs[name]
		if ok && std.ToolManifestHash(cur) == s.pinned[name] {
			continue
		}
		s.disabled[name] = true
		if !s.counted[name] && s.telemetry != nil {
			s.counted[name] = true
			s.telemetry.Count(ctx, manifestDriftMetric, 1, types.String("source", source))
		}
	}
	next := map[string]*importedTool{}
	for name, tool := range built {
		if s.disabled[name] {
			continue
		}
		next[name] = tool
	}
	s.tools = next
	return nil
}

func specList(m map[string]types.ToolSpec) []types.ToolSpec {
	out := make([]types.ToolSpec, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	return out
}
