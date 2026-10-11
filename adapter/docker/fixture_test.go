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
}

// NetworkFixture provisions controlled name resolution and live
// listeners on an isolated fixture network. It registers bounded
// cleanup on t and binds the factory's next Sandbox to the resources.
func (f *Factory) NetworkFixture(t *testing.T) sandboxtest.NetworkFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f.mu.Lock()
	defer f.mu.Unlock()
	fx, err := f.provisionFixture(ctx)
	if err != nil {
		t.Fatalf("docker: provision network fixture: %v", err)
	}
	t.Cleanup(func() { f.dropFixture(fx) })
	f.fx = fx
	f.fxNetIDs = []string{fx.hostNetID, fx.privNetID}
	return sandboxtest.NetworkFixture{PrivateHost: fixturePrivAddr, PrivatePort: fixturePrivPort}
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
