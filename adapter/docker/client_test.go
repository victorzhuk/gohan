package docker

import (
	"context"
	"testing"
)

func TestClientConnect(t *testing.T) {
	c, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()
	if c.BaseImage() != DefaultBaseImage {
		t.Fatalf("base image = %q, want default %q", c.BaseImage(), DefaultBaseImage)
	}
	if err := c.Handshake(context.Background()); err != nil {
		t.Fatalf("handshake: %v", err)
	}
}
