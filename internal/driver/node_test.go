package driver

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zhujian7/csi-demo/internal/backend"
)

func TestNodeGetInfoReportsNodeName(t *testing.T) {
	resp, err := newTestNodeServer(t).NodeGetInfo(context.Background(), &csi.NodeGetInfoRequest{})
	if err != nil {
		t.Fatalf("NodeGetInfo: %v", err)
	}
	if got, want := resp.GetNodeId(), "test-node"; got != want {
		t.Errorf("node id = %q, want %q", got, want)
	}
}

func TestNewNodeServerRequiresNodeName(t *testing.T) {
	t.Setenv("NODE_NAME", "")

	if _, err := NewNodeServer(backend.NewLocalDir(t.TempDir())); err == nil {
		t.Error("NewNodeServer accepted an empty NODE_NAME")
	}
}

func newTestNodeServer(t *testing.T) *NodeServer {
	t.Helper()
	t.Setenv("NODE_NAME", "test-node")

	srv, err := NewNodeServer(backend.NewLocalDir(t.TempDir()))
	if err != nil {
		t.Fatalf("NewNodeServer: %v", err)
	}
	return srv
}

func mountCapability() *csi.VolumeCapability {
	return &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}
}

func TestNodeGetCapabilitiesAdvertisesStageUnstage(t *testing.T) {
	resp, err := newTestNodeServer(t).NodeGetCapabilities(context.Background(), &csi.NodeGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("NodeGetCapabilities: %v", err)
	}
	var found bool
	for _, c := range resp.GetCapabilities() {
		if c.GetRpc().GetType() == csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME {
			found = true
		}
	}
	if !found {
		t.Error("STAGE_UNSTAGE_VOLUME capability not advertised")
	}
}

func TestNodeStageVolumeRejectsEmptyVolumeId(t *testing.T) {
	_, err := newTestNodeServer(t).NodeStageVolume(context.Background(), &csi.NodeStageVolumeRequest{
		StagingTargetPath: "/tmp/staging",
		VolumeCapability:  mountCapability(),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("got %v, want InvalidArgument", err)
	}
}

func TestNodePublishVolumeRejectsBlockVolumes(t *testing.T) {
	blockCapability := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}
	_, err := newTestNodeServer(t).NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId:         "vol-1",
		TargetPath:       "/tmp/target",
		VolumeCapability: blockCapability,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("got %v, want InvalidArgument", err)
	}
}
