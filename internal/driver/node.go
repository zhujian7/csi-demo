package driver

import (
	"context"
	"fmt"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zhujian7/csi-demo/internal/backend"
	"github.com/zhujian7/csi-demo/internal/mountutil"
)

// NodeServer runs on every node and does the local work kubelet asks for:
// mounting a volume so a pod can use it. Task 1 already gave it NodeGetInfo
// and a bare NodeGetCapabilities; this task adds the volume methods and the
// stage/unstage capability.
type NodeServer struct {
	csi.UnimplementedNodeServer

	backend backend.Backend
	nodeID  string
}

func NewNodeServer(be backend.Backend) (*NodeServer, error) {
	// The node name comes from the downward API rather than os.Hostname: a
	// pod's hostname is its own name, not the node it runs on.
	nodeID := os.Getenv("NODE_NAME")
	if nodeID == "" {
		return nil, fmt.Errorf("NODE_NAME environment variable is required")
	}
	return &NodeServer{backend: be, nodeID: nodeID}, nil
}

func (s *NodeServer) NodeGetInfo(_ context.Context, _ *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: s.nodeID}, nil
}

// NodeGetCapabilities now advertises stage/unstage: the backend prepares the
// volume once at a staging path, and every pod binds from there.
func (s *NodeServer) NodeGetCapabilities(_ context.Context, _ *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
				},
			},
		},
	}, nil
}

func (s *NodeServer) NodeStageVolume(_ context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume id is required")
	}
	if req.GetStagingTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "staging target path is required")
	}
	if err := validateMountCapability(req.GetVolumeCapability()); err != nil {
		return nil, err
	}

	if err := s.backend.Stage(req.GetVolumeId(), req.GetStagingTargetPath(), isReadOnly(req.GetVolumeCapability())); err != nil {
		return nil, status.Errorf(codes.Internal, "stage volume %s: %v", req.GetVolumeId(), err)
	}
	return &csi.NodeStageVolumeResponse{}, nil
}

func (s *NodeServer) NodeUnstageVolume(_ context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume id is required")
	}
	if req.GetStagingTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "staging target path is required")
	}

	if err := s.backend.Unstage(req.GetVolumeId(), req.GetStagingTargetPath()); err != nil {
		return nil, status.Errorf(codes.Internal, "unstage volume %s: %v", req.GetVolumeId(), err)
	}
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (s *NodeServer) NodePublishVolume(_ context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume id is required")
	}
	if req.GetTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "target path is required")
	}
	if err := validateMountCapability(req.GetVolumeCapability()); err != nil {
		return nil, err
	}

	source, err := s.backend.MountSource(req.GetVolumeId(), req.GetStagingTargetPath())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "resolve mount source for volume %s: %v", req.GetVolumeId(), err)
	}
	// if err := bindMount(source, req.GetTargetPath(), req.GetReadonly()); err != nil {
	if err := mountutil.BindMount(source, req.GetTargetPath(), req.GetReadonly()); err != nil {
		return nil, status.Errorf(codes.Internal, "bind mount %s at %s: %v", source, req.GetTargetPath(), err)
	}
	return &csi.NodePublishVolumeResponse{}, nil
}

func (s *NodeServer) NodeUnpublishVolume(_ context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume id is required")
	}
	if req.GetTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "target path is required")
	}

	// if err := unmount(req.GetTargetPath()); err != nil {
	if err := mountutil.Unmount(req.GetTargetPath()); err != nil {
		return nil, status.Errorf(codes.Internal, "unmount %s: %v", req.GetTargetPath(), err)
	}
	// kubelet expects the target path to be gone so a later publish starts clean.
	if err := os.Remove(req.GetTargetPath()); err != nil && !os.IsNotExist(err) {
		return nil, status.Errorf(codes.Internal, "remove target path %s: %v", req.GetTargetPath(), err)
	}
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// validateMountCapability rejects what this driver does not serve. kubelet
// passes the capability through from the PV, so a block volume or a missing
// access mode has to fail loudly here rather than mount something wrong.
func validateMountCapability(cap *csi.VolumeCapability) error {
	if cap == nil {
		return status.Error(codes.InvalidArgument, "volume capability is required")
	}
	if cap.GetMount() == nil {
		return status.Error(codes.InvalidArgument, "only mount volumes are supported, not block")
	}
	if cap.GetAccessMode() == nil {
		return status.Error(codes.InvalidArgument, "access mode is required")
	}
	return nil
}

// isReadOnly reports whether the capability asks for read-only access.
// NodeStageVolumeRequest carries no readonly flag of its own — unlike
// NodePublishVolumeRequest — so stage has to read it out of the access mode.
func isReadOnly(cap *csi.VolumeCapability) bool {
	switch cap.GetAccessMode().GetMode() {
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:
		return true
	default:
		return false
	}
}
