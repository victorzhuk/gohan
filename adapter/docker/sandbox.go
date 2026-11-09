package docker

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/types"
)

const (
	// LabelSandbox marks every container the adapter created.
	LabelSandbox = "gohan.sandbox"
	// LabelSnapshot marks committed snapshot images.
	LabelSnapshot = "gohan.sandbox.snapshot"
)

// Ownership labels bind every container and network of one workload
// group: the role separates workload, anchor and network resources, the
// owner names the never-reused group and the network label records the
// private network identity on both containers.
const (
	labelRole    = "gohan.sandbox.role"
	labelOwner   = "gohan.sandbox.owner"
	labelNetwork = "gohan.sandbox.network"
)

const (
	roleWorkload = "workload"
	roleAnchor   = "anchor"
	roleNetwork  = "network"
)

// workloadRefPrefix is the versioned encoded identity a workload
// ResourceRef carries: prefix, full container ID, immutable owner name.
const workloadRefPrefix = "gohan-workload:v1:"

// adminTimeout bounds the complete trusted policy installation
// independently of the workload's own execution policy.
const adminTimeout = 10 * time.Second

// newOwnerName mints one never-reused workload group identity: the
// "gohan-workload-" prefix over 32 lowercase hex digits of 16
// crypto/rand bytes, the shape parseWorkloadRef enforces.
func newOwnerName() string {
	var b [16]byte
	_, _ = crand.Read(b[:])
	return "gohan-workload-" + hex.EncodeToString(b[:])
}

// Sandbox opens Docker workspaces under a policy.
type Sandbox struct {
	c   *Client
	rec *Recorder
	// fxNetIDs are the optional conformance fixture networks. When set,
	// bridge workspaces also attach to them, so the normative allowlist
	// names resolve to the fixture listeners without public DNS.
	fxNetIDs []string
}

// New connects to the daemon from the environment, handshakes, and returns
// a Sandbox creating fresh workspaces from baseImage.
func New(ctx context.Context, baseImage string) (*Sandbox, error) {
	c, err := NewClient(baseImage)
	if err != nil {
		return nil, err
	}
	if err := c.Handshake(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return &Sandbox{c: c, rec: NewRecorder()}, nil
}

// Open is the only factory. An empty ref creates and starts a fresh
// container from the base image; a non-empty ref names a snapshot image
// (D5) the container is created from. A ref whose image does not exist is
// an error naming the ref; no fresh workspace is opened for it.
func (s *Sandbox) Open(ctx context.Context, ref types.WorkspaceRef, p types.SandboxPolicy) (types.Workspace, error) {
	image := s.c.BaseImage()
	if ref != "" {
		image = string(ref)
		if _, err := s.c.api.ImageInspect(ctx, image); err != nil {
			return nil, fmt.Errorf("docker: snapshot %q not found: %w", ref, err)
		}
	}
	w := &workspace{
		api:       s.c.api,
		policy:    p,
		rec:       s.rec,
		ownerName: newOwnerName(),
		env:       map[string]string{},
	}
	if len(p.Egress.Allow) == 0 {
		// Deny-all needs no firewall: NetworkMode none with no anchor,
		// no capabilities and no inherited startup surface.
		if err := w.createWorkload(ctx, image, p, "none", ""); err != nil {
			return nil, err
		}
		if _, err := s.c.api.ContainerStart(ctx, w.containerID, client.ContainerStartOptions{}); err != nil {
			_ = w.unwindGroup()
			return nil, fmt.Errorf("docker: start container %s: %w", w.containerID, err)
		}
		return w, nil
	}
	if err := s.openEgressGroup(ctx, w, image, p); err != nil {
		return nil, err
	}
	return w, nil
}

// openEgressGroup runs the trusted setup: a labelled private network, a
// NET_ADMIN anchor from the configured base image and the workload
// sharing the anchor's network namespace. Both containers exist stopped
// and associated before the anchor starts; the complete policy installs
// through a private ten-second-bounded administration runner against the
// anchor only, and the workload starts only after the chain is ready.
// Any failure unwinds every created resource with fresh bounded contexts
// and surfaces both the setup and the cleanup errors.
func (s *Sandbox) openEgressGroup(ctx context.Context, w *workspace, image string, p types.SandboxPolicy) error {
	netName, netID, err := s.setupEgress(ctx, p, w.ownerName)
	if err != nil {
		return err
	}
	w.netID = netName
	unwind := func(setupErr error) error {
		return errors.Join(setupErr, w.unwindGroup())
	}
	anchor, err := s.createAnchor(ctx, w, netName, netID, p)
	if err != nil {
		return unwind(fmt.Errorf("docker: create anchor: %w", err))
	}
	w.anchorID = anchor.ID
	if err := w.createWorkload(ctx, image, p, container.NetworkMode("container:"+anchor.ID), netID); err != nil {
		return unwind(fmt.Errorf("docker: create workload from %s: %w", image, err))
	}
	if _, err := s.c.api.ContainerStart(ctx, anchor.ID, client.ContainerStartOptions{}); err != nil {
		return unwind(fmt.Errorf("docker: start anchor %s: %w", anchor.ID, err))
	}
	if err := w.installEgressChain(ctx); err != nil {
		return unwind(err)
	}
	w.setChainReady(true)
	if _, err := s.c.api.ContainerStart(ctx, w.containerID, client.ContainerStartOptions{}); err != nil {
		return unwind(fmt.Errorf("docker: start container %s: %w", w.containerID, err))
	}
	return nil
}

// createAnchor builds the stopped trusted helper: an idle container from
// the configured base image (never from a restored snapshot or the
// workload image) carrying NET_ADMIN, attached to the private network
// and any fixture networks.
func (s *Sandbox) createAnchor(ctx context.Context, w *workspace, netName, netID string, p types.SandboxPolicy) (client.ContainerCreateResult, error) {
	cfg := &container.Config{
		Image:       s.c.BaseImage(),
		Entrypoint:  []string{"sleep", "infinity"},
		Healthcheck: &container.HealthConfig{Test: []string{"NONE"}},
		Labels: map[string]string{
			LabelSandbox: "true",
			labelRole:    roleAnchor,
			labelOwner:   w.ownerName,
			labelNetwork: netID,
		},
	}
	host := &container.HostConfig{
		Resources: resources(p),
		CapDrop:   []string{"ALL"},
		CapAdd:    []string{capNetAdmin},
	}
	netCfg := attachEgress(host, netName, s.fxNetIDs)
	created, err := s.c.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:           cfg,
		HostConfig:       host,
		NetworkingConfig: netCfg,
	})
	if err != nil {
		return client.ContainerCreateResult{}, err
	}
	return created, nil
}

// resources maps the policy's CPU and Memory strings onto HostConfig
// limits. Unset or unparsable values leave the limit off.
func resources(p types.SandboxPolicy) container.Resources {
	var r container.Resources
	if cpu, ok := parseCPU(p.CPU); ok {
		r.NanoCPUs = cpu
	}
	if mem, ok := parseBytes(p.Memory); ok {
		r.Memory = mem
	}
	return r
}

func parseCPU(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	mult := float64(1)
	if strings.HasSuffix(s, "m") {
		mult, s = 0.001, strings.TrimSuffix(s, "m")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	return int64(f * mult * 1e9), true
}

func parseBytes(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	mult := int64(1)
	for suffix, m := range map[string]int64{"k": 1 << 10, "m": 1 << 20, "g": 1 << 30, "t": 1 << 40} {
		if strings.HasSuffix(strings.ToLower(s), suffix) {
			mult, s = m, strings.TrimSuffix(strings.ToLower(s), suffix)
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n * mult, true
}
