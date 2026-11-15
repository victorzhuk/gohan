package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
)

// ErrNamespaceRequired is returned by Assemble when more than one set is
// combined and a set was built without a namespace: unprefixed names from
// two servers could collide.
var ErrNamespaceRequired = errors.New("mcp: namespace required to assemble multiple sets")

var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,15}$`)

// ClientOption adjusts one imported server set at construction.
type ClientOption func(*clientConfig)

type clientConfig struct {
	ns              string
	nsErr           error
	policy          gohan.ToolPolicy
	policySet       bool
	pinned          []byte
	credentials     types.CredentialSource
	callTimeout     time.Duration
	telemetry       types.Telemetry
	refreshInterval time.Duration
	cps             stores.Checkpoints
	taskPoll        time.Duration
}

// Namespace prefixes every imported tool name; it must match
// ^[a-z][a-z0-9]{0,15}$ and an invalid value fails construction.
func Namespace(ns string) ClientOption {
	return func(c *clientConfig) {
		if !namespacePattern.MatchString(ns) {
			c.nsErr = fmt.Errorf("mcp: invalid namespace %q", ns)
			return
		}
		c.ns = ns
	}
}

// WithPolicy sets the wiring-time trust policy. The default trusts nothing
// and caps effect at ReadOnly.
func WithPolicy(p gohan.ToolPolicy) ClientOption {
	return func(c *clientConfig) {
		c.policy = p
		c.policySet = true
	}
}

// WithPinnedManifest pins the imported catalog to a previously accepted
// manifest; a tool whose hash changes or disappears is disabled.
func WithPinnedManifest(data []byte) ClientOption {
	return func(c *clientConfig) { c.pinned = data }
}

// WithCredentials records the credential source used for authenticated
// server access.
func WithCredentials(cs types.CredentialSource) ClientOption {
	return func(c *clientConfig) { c.credentials = cs }
}

// WithCallTimeout bounds each imported tool call.
func WithCallTimeout(d time.Duration) ClientOption {
	return func(c *clientConfig) { c.callTimeout = d }
}

// WithTelemetry records metric emission for the set.
func WithTelemetry(t types.Telemetry) ClientOption {
	return func(c *clientConfig) { c.telemetry = t }
}

// WithRefreshInterval overrides the catalog refresh period for tests;
// production uses the server TTL hint, or one second when unset.
func WithRefreshInterval(d time.Duration) ClientOption {
	return func(c *clientConfig) { c.refreshInterval = d }
}

// Client connects to one MCP server and returns its tool set. The connection
// opens once here; the first catalog fetch happens at Tools.
func Client(url string, opts ...ClientOption) (*Set, error) {
	cfg := &clientConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.nsErr != nil {
		return nil, cfg.nsErr
	}
	if cfg.cps == nil {
		return nil, ErrCheckpointsRequired
	}
	if cfg.taskPoll == 0 {
		cfg.taskPoll = defaultTaskPoll
	}
	policy := cfg.policy
	if !cfg.policySet {
		policy = gohan.ToolPolicy{Trust: types.Untrusted, MaxEffect: types.ReadOnly}
	}
	var pinned map[string]string
	if cfg.pinned != nil {
		m, err := std.ParseManifest(cfg.pinned)
		if err != nil {
			return nil, fmt.Errorf("mcp: pinned manifest: %w", err)
		}
		pinned = m
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "gohan-mcp", Version: "v0.3.0"},
		&mcp.ClientOptions{MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}})
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: connect %s: %w", url, err)
	}
	return &Set{
		url:             url,
		ns:              cfg.ns,
		session:         session,
		cps:             cfg.cps,
		policy:          policy,
		pinned:          pinned,
		credentials:     cfg.credentials,
		callTimeout:     cfg.callTimeout,
		telemetry:       cfg.telemetry,
		refreshInterval: cfg.refreshInterval,
		tools:           map[string]*importedTool{},
		disabled:        map[string]bool{},
		counted:         map[string]bool{},
		done:            make(chan struct{}),
	}, nil
}

// Assemble combines several imported sets into one registration list. Every
// set in a multi-set assembly must carry a namespace.
func Assemble(sets ...*Set) ([]types.Tool, error) {
	if len(sets) > 1 {
		for _, s := range sets {
			if s.ns == "" {
				return nil, fmt.Errorf("%w: %s", ErrNamespaceRequired, s.url)
			}
		}
	}
	var out []types.Tool
	for _, s := range sets {
		tools, err := s.Tools(context.Background())
		if err != nil {
			return nil, err
		}
		out = append(out, tools...)
	}
	return out, nil
}

// importedTool is one server tool registered into gohan as deferred and
// untrusted.
type importedTool struct {
	spec        types.ToolSpec
	session     *mcp.ClientSession
	serverName  string
	callTimeout time.Duration
	cps         stores.Checkpoints
}

func (t *importedTool) Spec() types.ToolSpec { return t.spec }

func (t *importedTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	if t.callTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.callTimeout)
		defer cancel()
	}
	res, err := t.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      t.serverName,
		Arguments: json.RawMessage(args),
	})
	if err != nil {
		return types.ToolResult{}, err
	}
	text := joinText(res)
	if res.NeedsInput() {
		susp, serr := t.suspendInput(ctx, args, res)
		if serr != nil {
			return types.ToolResult{}, serr
		}
		return types.ToolResult{}, susp
	}
	if h, ok := taskMetaHandle(res, t.serverName); ok {
		susp, serr := t.suspendTask(ctx, args, h)
		if serr != nil {
			return types.ToolResult{}, serr
		}
		return types.ToolResult{}, susp
	}
	if res.IsError {
		return types.ToolResult{
			Outcome: types.Failed,
			Error:   &types.ToolError{Kind: types.Permanent, Message: text},
		}, nil
	}
	return types.ToolResult{
		Outcome: types.Succeeded,
		Content: []types.Block{types.Text{Text: text}},
	}, nil
}

func joinText(res *mcp.CallToolResult) string {
	out := ""
	for _, c := range res.Content {
		tc, ok := c.(*mcp.TextContent)
		if !ok {
			continue
		}
		if out != "" {
			out += "\n"
		}
		out += tc.Text
	}
	return out
}

func (s *Set) buildTool(t *mcp.Tool) (*importedTool, types.ToolSpec, error) {
	schema, err := json.Marshal(t.InputSchema)
	if err != nil {
		return nil, types.ToolSpec{}, fmt.Errorf("mcp: tool %s schema: %w", t.Name, err)
	}
	name := t.Name
	if s.ns != "" {
		name = s.ns + "__" + t.Name
	}
	effective := s.policy.MaxEffect
	if cap, ok := AnnotationCap(t.Annotations); ok {
		effective = minEffect(effective, cap)
	}
	spec := types.ToolSpec{
		Name:        name,
		Description: t.Description,
		Schema:      json.RawMessage(schema),
		Effect:      effective,
		Deferred:    true,
		Trust:       types.Untrusted,
		Executor:    types.ByHarness,
		Timeout:     s.callTimeout,
	}
	spec, _ = gohan.ApplyToolPolicy(spec, gohan.ToolPolicy{Trust: types.Untrusted, MaxEffect: effective})
	return &importedTool{
		spec:        spec,
		session:     s.session,
		serverName:  t.Name,
		callTimeout: s.callTimeout,
		cps:         s.cps,
	}, spec, nil
}
