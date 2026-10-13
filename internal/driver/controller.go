package driver

import (
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zhujian7/csi-demo/internal/backend"
)

// minimumVolumeBytes is what a volume gets when the caller states no size.
const minimumVolumeBytes = 64 << 20

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
	if limit := req.GetCapacityRange().GetLimitBytes(); limit > 0 && limit < req.GetCapacityRange().GetRequiredBytes() {
		return nil, status.Errorf(codes.InvalidArgument,
			"capacity range limit %d is smaller than required %d", limit, req.GetCapacityRange().GetRequiredBytes())
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

	// volume_capabilities is required: an empty list is a malformed request, and
	// answering "not supported" would hide that from the caller.
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one volume capability is required")
	}

	// A well-formed capability this driver does not serve is a negative answer,
	// not an error: the caller asked a question and the answer is no.
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

// requestedBytes picks the size to provision and keeps it inside the range the
// caller allowed: at least required, never above limit, and at least
// minimumVolumeBytes so a caller that states no size still gets a usable volume.
func requestedBytes(capacityRange *csi.CapacityRange) int64 {
	required := capacityRange.GetRequiredBytes()
	limit := capacityRange.GetLimitBytes()

	size := required
	if size <= 0 {
		size = limit
	}
	if size < minimumVolumeBytes {
		size = minimumVolumeBytes
	}
	if limit > 0 && size > limit {
		size = limit
	}
	return size
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
