package driver

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestNodeGetInfoReportsNodeName(t *testing.T) {
	t.Setenv("NODE_NAME", "test-node")

	srv, err := NewNodeServer()
	if err != nil {
		t.Fatalf("NewNodeServer: %v", err)
	}
	resp, err := srv.NodeGetInfo(context.Background(), &csi.NodeGetInfoRequest{})
	if err != nil {
		t.Fatalf("NodeGetInfo: %v", err)
	}
	if got, want := resp.GetNodeId(), "test-node"; got != want {
		t.Errorf("node id = %q, want %q", got, want)
	}
}

func TestNewNodeServerRequiresNodeName(t *testing.T) {
	t.Setenv("NODE_NAME", "")

	if _, err := NewNodeServer(); err == nil {
		t.Error("NewNodeServer accepted an empty NODE_NAME")
	}
}
