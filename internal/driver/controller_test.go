package driver

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zhujian7/csi-demo/internal/backend"
)

func newTestControllerServer(t *testing.T) *ControllerServer {
	t.Helper()
	return NewControllerServer(backend.NewLocalDir(t.TempDir()))
}

func createRequest(name string, bytes int64) *csi.CreateVolumeRequest {
	return &csi.CreateVolumeRequest{
		Name:               name,
		VolumeCapabilities: []*csi.VolumeCapability{mountCapability()},
		CapacityRange:      &csi.CapacityRange{RequiredBytes: bytes},
	}
}

func TestCreateVolumeReturnsAHandle(t *testing.T) {
	resp, err := newTestControllerServer(t).CreateVolume(context.Background(), createRequest("my-pvc", 1<<30))
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if resp.GetVolume().GetVolumeId() == "" {
		t.Error("CreateVolume returned an empty volume id")
	}
	if got := resp.GetVolume().GetCapacityBytes(); got != 1<<30 {
		t.Errorf("capacity = %d, want %d", got, 1<<30)
	}
}

func TestCreateVolumeIsIdempotentOnName(t *testing.T) {
	s := newTestControllerServer(t)

	first, err := s.CreateVolume(context.Background(), createRequest("my-pvc", 1<<30))
	if err != nil {
		t.Fatalf("first CreateVolume: %v", err)
	}
	second, err := s.CreateVolume(context.Background(), createRequest("my-pvc", 1<<30))
	if err != nil {
		t.Fatalf("second CreateVolume: %v", err)
	}
	if first.GetVolume().GetVolumeId() != second.GetVolume().GetVolumeId() {
		t.Errorf("retry produced a second volume: %s != %s",
			first.GetVolume().GetVolumeId(), second.GetVolume().GetVolumeId())
	}
}

func TestCreateVolumeRejectsRequestLargerThanExistingVolume(t *testing.T) {
	s := newTestControllerServer(t)

	if _, err := s.CreateVolume(context.Background(), createRequest("my-pvc", 1<<30)); err != nil {
		t.Fatalf("first CreateVolume: %v", err)
	}
	// The existing volume is smaller than required, so it cannot satisfy the
	// request: the caller's view of the world is stale.
	_, err := s.CreateVolume(context.Background(), createRequest("my-pvc", 2<<30))
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("got %v, want AlreadyExists", err)
	}
}

func TestCreateVolumeAcceptsRequestSmallerThanExistingVolume(t *testing.T) {
	s := newTestControllerServer(t)

	created, err := s.CreateVolume(context.Background(), createRequest("my-pvc", 2<<30))
	if err != nil {
		t.Fatalf("first CreateVolume: %v", err)
	}
	// A smaller request is satisfied by the existing volume — it is at least
	// the required size — so this is a compatible retry, not a conflict.
	again, err := s.CreateVolume(context.Background(), createRequest("my-pvc", 1<<30))
	if err != nil {
		t.Fatalf("second CreateVolume: %v", err)
	}
	if again.GetVolume().GetVolumeId() != created.GetVolume().GetVolumeId() {
		t.Errorf("got a different volume: %s != %s",
			again.GetVolume().GetVolumeId(), created.GetVolume().GetVolumeId())
	}
}

func TestCreateVolumeRejectsBlockCapability(t *testing.T) {
	blockCapability := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}
	_, err := newTestControllerServer(t).CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "my-pvc",
		VolumeCapabilities: []*csi.VolumeCapability{blockCapability},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("got %v, want InvalidArgument", err)
	}
}

func TestDeleteVolumeIsIdempotent(t *testing.T) {
	s := newTestControllerServer(t)

	created, err := s.CreateVolume(context.Background(), createRequest("my-pvc", 1<<30))
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	id := created.GetVolume().GetVolumeId()

	if _, err := s.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{VolumeId: id}); err != nil {
		t.Fatalf("first DeleteVolume: %v", err)
	}
	if _, err := s.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{VolumeId: id}); err != nil {
		t.Errorf("second DeleteVolume returned %v, want nil", err)
	}
}

func TestValidateVolumeCapabilitiesConfirmsSupportedVolume(t *testing.T) {
	s := newTestControllerServer(t)

	created, err := s.CreateVolume(context.Background(), createRequest("my-pvc", 1<<30))
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	resp, err := s.ValidateVolumeCapabilities(context.Background(), &csi.ValidateVolumeCapabilitiesRequest{
		VolumeId:           created.GetVolume().GetVolumeId(),
		VolumeCapabilities: []*csi.VolumeCapability{mountCapability()},
	})
	if err != nil {
		t.Fatalf("ValidateVolumeCapabilities: %v", err)
	}
	if resp.GetConfirmed() == nil {
		t.Error("supported capability was not confirmed")
	}
}

func TestValidateVolumeCapabilitiesReportsUnknownVolume(t *testing.T) {
	_, err := newTestControllerServer(t).ValidateVolumeCapabilities(context.Background(), &csi.ValidateVolumeCapabilitiesRequest{
		VolumeId:           "no-such-volume",
		VolumeCapabilities: []*csi.VolumeCapability{mountCapability()},
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("got %v, want NotFound", err)
	}
}

func TestControllerGetCapabilitiesAdvertisesCreateDelete(t *testing.T) {
	resp, err := newTestControllerServer(t).ControllerGetCapabilities(context.Background(),
		&csi.ControllerGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("ControllerGetCapabilities: %v", err)
	}
	var found bool
	for _, c := range resp.GetCapabilities() {
		if c.GetRpc().GetType() == csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME {
			found = true
		}
	}
	if !found {
		t.Error("CREATE_DELETE_VOLUME capability not advertised")
	}
}

func TestControllerGetCapabilitiesAdvertisesPublishUnpublish(t *testing.T) {
	resp, err := newTestControllerServer(t).ControllerGetCapabilities(context.Background(), &csi.ControllerGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("ControllerGetCapabilities: %v", err)
	}
	var found bool
	for _, c := range resp.GetCapabilities() {
		if c.GetRpc().GetType() == csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME {
			found = true
		}
	}
	if !found {
		t.Error("PUBLISH_UNPUBLISH_VOLUME capability not advertised")
	}
}

func TestControllerPublishVolumeIsIdempotent(t *testing.T) {
	s := newTestControllerServer(t)

	created, err := s.CreateVolume(context.Background(), createRequest("my-pvc", 1<<30))
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	req := &csi.ControllerPublishVolumeRequest{
		VolumeId:         created.GetVolume().GetVolumeId(),
		NodeId:           "worker-1",
		VolumeCapability: mountCapability(),
	}

	first, err := s.ControllerPublishVolume(context.Background(), req)
	if err != nil {
		t.Fatalf("first ControllerPublishVolume: %v", err)
	}
	second, err := s.ControllerPublishVolume(context.Background(), req)
	if err != nil {
		t.Fatalf("second ControllerPublishVolume: %v", err)
	}
	if first.GetPublishContext()["devicePath"] != second.GetPublishContext()["devicePath"] {
		t.Error("retry returned a different publish context")
	}
}

func TestControllerPublishVolumeReportsUnknownVolume(t *testing.T) {
	_, err := newTestControllerServer(t).ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId:         "no-such-volume",
		NodeId:           "worker-1",
		VolumeCapability: mountCapability(),
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("got %v, want NotFound", err)
	}
}
