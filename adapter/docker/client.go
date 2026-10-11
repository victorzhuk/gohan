// Package docker runs gohan sandboxes as Docker containers. It implements
// the types.Sandbox port over the pinned moby client, enforcing the policy
// at the container layer: egress at the network layer, process time at the
// exec layer, and persistence as committed, labelled images.
package docker

import (
	"context"
	"fmt"
	"time"

	"github.com/moby/moby/client"
)

// DefaultBaseImage is the base image Open uses for a fresh workspace when
// the factory names none. It carries a POSIX shell and the common tools
// the probe vocabulary needs.
const DefaultBaseImage = "alpine:3.23"

// Client is a connected Docker API client with the adapter's base image.
type Client struct {
	api       *client.Client
	baseImage string
}

// NewClient builds the API client from the environment (DOCKER_HOST or the
// default socket) with API version negotiation.
func NewClient(baseImage string) (*Client, error) {
	if baseImage == "" {
		baseImage = DefaultBaseImage
	}
	api, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker: build client: %w", err)
	}
	return &Client{api: api, baseImage: baseImage}, nil
}

// Handshake pings the daemon and negotiates the API version. It fails fast
// when no daemon is reachable.
func (c *Client) Handshake(ctx context.Context) error {
	kctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ping, err := c.api.Ping(kctx, client.PingOptions{NegotiateAPIVersion: true})
	if err != nil {
		return fmt.Errorf("docker: daemon unreachable at %s: %w", c.api.DaemonHost(), err)
	}
	if ping.APIVersion == "" {
		return fmt.Errorf("docker: daemon at %s reported no API version", c.api.DaemonHost())
	}
	return nil
}

// BaseImage is the image a fresh workspace is created from.
func (c *Client) BaseImage() string { return c.baseImage }

// Close releases the underlying API client.
func (c *Client) Close() error { return c.api.Close() }
