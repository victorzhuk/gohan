package docker

import (
	"context"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/testkit/sandboxtest"
)

// fixtureNetNamePrefix prefixes the fixture network the Factory
// provisions for the conformance suite's network cases.
const fixtureNetNamePrefix = "gohan-fx-"

// The fixture subnets are isolated on the daemon and never routed to the
// internet: 198.18.0.0/16 is a non-private-class benchmark range the
// shared private-range rules permit, 10.250.250.0/24 is an RFC1918 range
// the DenyPrivate control needs. Neither name resolves to loopback or
// link-local, and no fixture address escapes onto the host's normal
// networks: the peers only talk over this bridge.
const (
	fixtureHostSubnet = "198.18.0.0/16"
	fixtureAPIAddr    = "198.18.0.10"
	fixtureOtherAddr  = "198.18.0.11"
	fixturePrivSubnet = "10.250.250.0/24"
	fixturePrivAddr   = "10.250.250.10"
	fixturePrivPort   = 8080
	fixtureHostPort   = 443
)

// fixtureResources are the provider-owned listeners the Factory
// provisions per network case and removes through the test's cleanup.
// api.example.com and other.example.com are container aliases, so the
// normative names resolve to live fixture listeners without public DNS.
type fixtureResources struct {
	hostNetID string
	privNetID string
	peers     []string
	// workloadImage names the committed image carrying iptables, so a
	// firewall-mutation attempt fails on capability, not on a missing
	// binary.
	workloadImage string
}

// NetworkFixture provisions controlled name resolution and live
// listeners on an isolated fixture network. It registers bounded
// cleanup on t and binds the factory's next Sandbox to the resources.
func (f *Factory) NetworkFixture(t *testing.T) sandboxtest.NetworkFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f.mu.Lock()
	fx, err := f.provisionFixture(ctx)
	if err != nil {
		f.mu.Unlock()
		t.Fatalf("docker: provision network fixture: %v", err)
	}
	f.fx = fx
	f.fxNetIDs = []string{fx.hostNetID, fx.privNetID}
	f.mu.Unlock()
	t.Cleanup(func() { f.dropFixtureChecked(t, fx) })
	// Provision the trusted attack utility before the measured scenario:
	// without iptables in the image a flush attempt fails on a missing
	// binary and proves nothing about kernel authority.
	fx.workloadImage, err = f.buildWorkloadImage(ctx)
	if err != nil {
		t.Fatalf("docker: build workload image: %v", err)
	}
	// Verify each hostname listener independently from the fixture side,
	// before any policy assertion can mistake a dead peer for enforcement.
	f.waitPeerReady(t, fx.peers[0], fixtureHostPort)
	f.waitPeerReady(t, fx.peers[1], fixtureHostPort)
	return sandboxtest.NetworkFixture{PrivateHost: fixturePrivAddr, PrivatePort: fixturePrivPort}
}

// WorkloadImage returns the iptables-provisioned image NetworkFixture
// built. It is valid only after NetworkFixture.
func (f *Factory) WorkloadImage() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fx.workloadImage
}

// buildWorkloadImage commits a copy of the base image with iptables
// installed, tagged as a fixture-owned image.
func (f *Factory) buildWorkloadImage(ctx context.Context) (string, error) {
	tag := fmt.Sprintf("%sworkload-%d", fixtureNetNamePrefix, time.Now().UnixNano())
	created, err := f.c.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  f.c.BaseImage(),
			Cmd:    []string{"apk", "add", "--no-cache", "iptables", "libcap"},
			Labels: map[string]string{LabelSandbox: "true"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("create workload build container: %w", err)
	}
	if _, err := f.c.api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		_, _ = f.c.api.ContainerRemove(ctx, created.ID, client.ContainerRemoveOptions{Force: true})
		return "", fmt.Errorf("start workload build container: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		insp, err := f.c.api.ContainerInspect(ctx, created.ID, client.ContainerInspectOptions{})
		if err != nil {
			return "", fmt.Errorf("inspect workload build container: %w", err)
		}
		if st := insp.Container.State; st != nil && !st.Running {
			if st.ExitCode != 0 {
				return "", fmt.Errorf("apk add iptables exit %d", st.ExitCode)
			}
			break
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("workload image build timed out")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := f.c.api.ContainerCommit(ctx, created.ID, client.ContainerCommitOptions{
		Reference: tag,
		NoPause:   true,
	}); err != nil {
		return "", fmt.Errorf("commit workload image: %w", err)
	}
	_, _ = f.c.api.ContainerRemove(ctx, created.ID, client.ContainerRemoveOptions{Force: true})
	return tag, nil
}

// waitPeerReady probes one peer listener over its own loopback until a
// full HTTP exchange with the expected status comes back, failing the
// test when the peer never serves one.
func (f *Factory) waitPeerReady(t *testing.T, id string, port int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	deadline := time.Now().Add(10 * time.Second)
	for {
		created, err := f.c.api.ExecCreate(ctx, id, client.ExecCreateOptions{
			Cmd: []string{"sh", "-c", fmt.Sprintf(
				"printf 'GET / HTTP/1.0\\r\\n\\r\\n' | nc -w 2 127.0.0.1 %d | grep -q 'HTTP/1.0 200'", port)},
		})
		if err == nil {
			if _, err := f.c.api.ExecStart(ctx, created.ID, client.ExecStartOptions{Detach: true}); err == nil {
				for {
					insp, err := f.c.api.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
					if err != nil {
						break
					}
					if !insp.Running {
						if insp.ExitCode == 0 {
							return
						}
						break
					}
					if time.Now().After(deadline) {
						break
					}
					time.Sleep(200 * time.Millisecond)
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture peer %s listener on port %d never answered a ready probe", id[:12], port)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// dropFixtureChecked removes the fixture-owned resources by their exact
// recorded IDs and asserts every removal succeeded.
func (f *Factory) dropFixtureChecked(t *testing.T, fx *fixtureResources) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, id := range fx.peers {
		if _, err := f.c.api.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true}); err != nil {
			t.Errorf("docker: remove fixture peer %s: %v", id, err)
		}
	}
	for _, netID := range []string{fx.hostNetID, fx.privNetID} {
		if netID == "" {
			continue
		}
		if _, err := f.c.api.NetworkRemove(ctx, netID, client.NetworkRemoveOptions{}); err != nil {
			t.Errorf("docker: remove fixture network %s: %v", netID, err)
		}
	}
	if fx.workloadImage != "" {
		if _, err := f.c.api.ImageRemove(ctx, fx.workloadImage, client.ImageRemoveOptions{}); err != nil {
			t.Errorf("docker: remove workload image %s: %v", fx.workloadImage, err)
		}
	}
}

// provisionFixture creates the fixture network and its three listeners.
// The hostname peers answer a full HTTP exchange, so a probe that
// reaches them can distinguish a live listener from a dropped dial.
func (f *Factory) provisionFixture(ctx context.Context) (*fixtureResources, error) {
	create := func(name string, subnet string) (string, error) {
		res, err := f.c.api.NetworkCreate(ctx, name, client.NetworkCreateOptions{
			Driver: "bridge",
			Labels: map[string]string{LabelSandbox: "true"},
			IPAM: &network.IPAM{Config: []network.IPAMConfig{
				{Subnet: netip.MustParsePrefix(subnet)},
			}},
		})
		if err != nil {
			return "", fmt.Errorf("create network: %w", err)
		}
		return res.ID, nil
	}
	hostNet, err := create(fmt.Sprintf("%shost-%d", fixtureNetNamePrefix, time.Now().UnixNano()), fixtureHostSubnet)
	if err != nil {
		return nil, err
	}
	privNet, err := create(fmt.Sprintf("%spriv-%d", fixtureNetNamePrefix, time.Now().UnixNano()), fixturePrivSubnet)
	if err != nil {
		f.dropFixture(&fixtureResources{hostNetID: hostNet})
		return nil, err
	}
	fx := &fixtureResources{hostNetID: hostNet, privNetID: privNet}
	start := func(cmd []string, netID string, addr string, aliases []string) error {
		created, err := f.c.api.ContainerCreate(ctx, client.ContainerCreateOptions{
			Config: &container.Config{
				Image:  f.c.BaseImage(),
				Cmd:    cmd,
				Labels: map[string]string{LabelSandbox: "true"},
			},
			NetworkingConfig: &network.NetworkingConfig{
				EndpointsConfig: map[string]*network.EndpointSettings{netID: {
					Aliases: aliases,
					IPAMConfig: &network.EndpointIPAMConfig{
						IPv4Address: netip.MustParseAddr(addr),
					},
				}},
			},
		})
		if err != nil {
			return fmt.Errorf("create fixture peer %v: %w", aliases, err)
		}
		fx.peers = append(fx.peers, created.ID)
		if _, err := f.c.api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
			return fmt.Errorf("start fixture peer %v: %w", aliases, err)
		}
		return nil
	}
	// Alpine's busybox has no httpd applet, so the peers answer one
	// HTTP request per nc connection.
	http := func(port int) []string {
		return []string{"sh", "-c", fmt.Sprintf(
			"while :; do printf 'HTTP/1.0 200 OK\\r\\n\\r\\nok' | nc -l -p %d >/dev/null; done", port)}
	}
	if err := start(http(fixtureHostPort), hostNet, fixtureAPIAddr, []string{"api.example.com"}); err != nil {
		f.dropFixture(fx)
		return nil, err
	}
	if err := start(http(fixtureHostPort), hostNet, fixtureOtherAddr, []string{"other.example.com"}); err != nil {
		f.dropFixture(fx)
		return nil, err
	}
	if err := start(http(fixturePrivPort), privNet, fixturePrivAddr, nil); err != nil {
		f.dropFixture(fx)
		return nil, err
	}
	return fx, nil
}

// dropFixture removes the fixture peers and network under a fresh
// bounded context.
func (f *Factory) dropFixture(fx *fixtureResources) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, id := range fx.peers {
		_, _ = f.c.api.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
	}
	for _, netID := range []string{fx.hostNetID, fx.privNetID} {
		if netID != "" {
			_, _ = f.c.api.NetworkRemove(ctx, netID, client.NetworkRemoveOptions{})
		}
	}
}
