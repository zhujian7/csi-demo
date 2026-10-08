package driver

import (
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zhujian7/csi-demo/internal/backend"
)

// ControllerServer runs cluster-wide. It decides that a volume should exist
// and mints the handle that identifies it from then on; the per-node work of
// making it usable happens in NodeServer.
type ControllerServer struct {
	csi.UnimplementedControllerServer

	backend backend.Backend
}

func NewControllerServer(be backend.Backend) *ControllerServer {
	return &ControllerServer{backend: be}
}

// ControllerGetCapabilities is mandatory for a driver that exposes the
// Controller service: the sidecars call it on startup and exit fatally when it
// is missing. Advertising CREATE_DELETE_VOLUME is what tells
// external-provisioner it may call CreateVolume.
func (s *ControllerServer) ControllerGetCapabilities(_ context.Context,
	_ *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: []*csi.ControllerServiceCapability{
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					},
				},
			},
		},
	}, nil
}

func (s *ControllerServer) CreateVolume(_ context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume name is required")
	}
	if err := validateCapabilities(req.GetVolumeCapabilities()); err != nil {
		return nil, err
	}

	capacity := requestedBytes(req.GetCapacityRange())

	// CreateVolume is retried: the same name must resolve to the same volume
	// rather than make a second one. Growing is refused because a caller that
	// asks for less than what exists has a stale view.
	existing, err := s.backend.FindByName(req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "look up volume %s: %v", req.GetName(), err)
	}
	if existing != nil {
		// A retry is compatible when the existing volume satisfies the requested
		// range: at least required_bytes, and no larger than limit_bytes when a
		// limit is given. A smaller request is therefore fine — the existing
		// volume is at least as big as asked — while a larger one is a conflict.
		required := req.GetCapacityRange().GetRequiredBytes()
		limit := req.GetCapacityRange().GetLimitBytes()
		if existing.CapacityBytes < required || (limit > 0 && existing.CapacityBytes > limit) {
			return nil, status.Errorf(codes.AlreadyExists,
				"volume %s already exists with capacity %d, which does not satisfy the requested range [%d, %d]",
				req.GetName(), existing.CapacityBytes, required, limit)
		}
		return createVolumeResponse(existing), nil
	}

	vol, err := s.backend.CreateVolume(req.GetName(), capacity)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create volume %s: %v", req.GetName(), err)
	}
	return createVolumeResponse(vol), nil
}

func (s *ControllerServer) DeleteVolume(_ context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume id is required")
	}
	if err := s.backend.DeleteVolume(req.GetVolumeId()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete volume %s: %v", req.GetVolumeId(), err)
	}
	return &csi.DeleteVolumeResponse{}, nil
}

func (s *ControllerServer) ValidateVolumeCapabilities(_ context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume id is required")
	}
	vol, err := s.backend.FindByID(req.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "look up volume %s: %v", req.GetVolumeId(), err)
	}
	if vol == nil {
		return nil, status.Errorf(codes.NotFound, "volume %s not found", req.GetVolumeId())
	}

	// An unsupported capability is a negative answer, not an error: the caller
	// asked a question and the answer is no.
	if err := validateCapabilities(req.GetVolumeCapabilities()); err != nil {
		return &csi.ValidateVolumeCapabilitiesResponse{}, nil
	}
	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: req.GetVolumeCapabilities(),
		},
	}, nil
}

func createVolumeResponse(vol *backend.Volume) *csi.CreateVolumeResponse {
	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      vol.ID,
			CapacityBytes: vol.CapacityBytes,
		},
	}
}

// requestedBytes picks the size to provision. A caller may give only a limit,
// and a PVC with no size at all leaves both zero.
func requestedBytes(capacityRange *csi.CapacityRange) int64 {
	if required := capacityRange.GetRequiredBytes(); required > 0 {
		return required
	}
	return capacityRange.GetLimitBytes()
}

func validateCapabilities(caps []*csi.VolumeCapability) error {
	if len(caps) == 0 {
		return status.Error(codes.InvalidArgument, "at least one volume capability is required")
	}
	for _, capability := range caps {
		if err := validateMountCapability(capability); err != nil {
			return err
		}
	}
	return nil
}
