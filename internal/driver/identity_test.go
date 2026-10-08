package driver

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestGetPluginInfoReturnsDriverName(t *testing.T) {
	s := NewIdentityServer("csi.demo.cubestack.io", "0.1.0")

	resp, err := s.GetPluginInfo(context.Background(), &csi.GetPluginInfoRequest{})
	if err != nil {
		t.Fatalf("GetPluginInfo returned error: %v", err)
	}
	if got, want := resp.GetName(), "csi.demo.cubestack.io"; got != want {
		t.Errorf("name = %q, want %q", got, want)
	}
	if got, want := resp.GetVendorVersion(), "0.1.0"; got != want {
		t.Errorf("vendor version = %q, want %q", got, want)
	}
}

func TestProbeReportsReady(t *testing.T) {
	s := NewIdentityServer("csi.demo.cubestack.io", "0.1.0")

	resp, err := s.Probe(context.Background(), &csi.ProbeRequest{})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if !resp.GetReady().GetValue() {
		t.Error("Probe reported not ready, want ready")
	}
}

func TestGetPluginCapabilitiesAdvertisesController(t *testing.T) {
	s := NewIdentityServer("csi.demo.cubestack.io", "0.1.0")

	resp, err := s.GetPluginCapabilities(context.Background(), &csi.GetPluginCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetPluginCapabilities returned error: %v", err)
	}
	var found bool
	for _, c := range resp.GetCapabilities() {
		if c.GetService().GetType() == csi.PluginCapability_Service_CONTROLLER_SERVICE {
			found = true
		}
	}
	if !found {
		t.Error("CONTROLLER_SERVICE capability not advertised")
	}
}
