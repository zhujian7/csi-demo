package driver

import (
	"context"
	"fmt"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

// NodeServer runs on every node. kubelet talks to it directly over a unix
// socket. At L0 it only has to identify the node well enough for
// node-driver-registrar to register the plugin; Task 2 adds the volume
// methods.
type NodeServer struct {
	csi.UnimplementedNodeServer

	nodeID string
}

func NewNodeServer() (*NodeServer, error) {
	// The node name comes from the downward API rather than os.Hostname: a
	// pod's hostname is its own name, not the node it runs on.
	nodeID := os.Getenv("NODE_NAME")
	if nodeID == "" {
		return nil, fmt.Errorf("NODE_NAME environment variable is required")
	}
	return &NodeServer{nodeID: nodeID}, nil
}

func (s *NodeServer) NodeGetInfo(_ context.Context, _ *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: s.nodeID}, nil
}

func (s *NodeServer) NodeGetCapabilities(_ context.Context, _ *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{}, nil
}
