package docker

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/types"
)

const (
	// LabelSandbox marks every container the adapter created.
	LabelSandbox = "gohan.sandbox"
	// LabelSnapshot marks committed snapshot images.
	LabelSnapshot = "gohan.sandbox.snapshot"
)

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
	cfg := &container.Config{
		Image:  image,
		Cmd:    []string{"sleep", "infinity"},
		Labels: map[string]string{LabelSandbox: "true"},
	}
	netName, capped, err := s.setupEgress(ctx, p)
	if err != nil {
		return nil, err
	}
	var netCfg *network.NetworkingConfig
	host := &container.HostConfig{Resources: resources(p)}
	if capped {
		netCfg = attachEgress(host, netName, s.fxNetIDs)
		host.CapAdd = append(host.CapAdd, capNetAdmin)
	} else {
		host.NetworkMode = "none"
	}
	created, err := s.c.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:           cfg,
		HostConfig:       host,
		NetworkingConfig: netCfg,
	})
	if err != nil {
		s.dropEgressNetwork(ctx, netName)
		return nil, fmt.Errorf("docker: create container from %s: %w", image, err)
	}
	w := &workspace{
		api:         s.c.api,
		containerID: created.ID,
		policy:      p,
		rec:         s.rec,
		netID:       netName,
		env:         map[string]string{},
	}
	if _, err := s.c.api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		_, _ = s.c.api.ContainerRemove(ctx, created.ID, client.ContainerRemoveOptions{Force: true})
		s.dropEgressNetwork(ctx, netName)
		return nil, fmt.Errorf("docker: start container %s: %w", created.ID, err)
	}
	if capped {
		if err := w.installEgressChain(ctx); err != nil {
			_, _ = s.c.api.ContainerRemove(ctx, created.ID, client.ContainerRemoveOptions{Force: true})
			s.dropEgressNetwork(ctx, netName)
			return nil, err
		}
		w.chainReady = true
	}
	return w, nil
}

// dropEgressNetwork removes a per-workspace network after its container
// failed to start.
func (s *Sandbox) dropEgressNetwork(ctx context.Context, netName string) {
	if netName != "" && netName != "none" {
		_, _ = s.c.api.NetworkRemove(ctx, netName, client.NetworkRemoveOptions{})
	}
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
