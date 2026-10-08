package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateVolumeIsIdempotentOnName(t *testing.T) {
	b := NewLocalDir(t.TempDir())

	first, err := b.CreateVolume("my-pvc", 1<<30)
	if err != nil {
		t.Fatalf("first CreateVolume: %v", err)
	}
	second, err := b.CreateVolume("my-pvc", 1<<30)
	if err != nil {
		t.Fatalf("second CreateVolume: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("CreateVolume is not idempotent: got ids %s and %s", first.ID, second.ID)
	}
}

func TestDeleteVolumeRemovesData(t *testing.T) {
	root := t.TempDir()
	b := NewLocalDir(root)

	vol, err := b.CreateVolume("my-pvc", 1<<30)
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if err := b.DeleteVolume(vol.ID); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, vol.ID)); !os.IsNotExist(err) {
		t.Errorf("volume directory still present after delete (stat err = %v)", err)
	}
}

func TestDeleteUnknownVolumeIsNotAnError(t *testing.T) {
	b := NewLocalDir(t.TempDir())
	if err := b.DeleteVolume("no-such-volume"); err != nil {
		t.Errorf("DeleteVolume on unknown id returned %v, want nil", err)
	}
}

func TestStageAdoptsUnknownVolumeForStaticProvisioning(t *testing.T) {
	root := t.TempDir()
	b := NewLocalDir(root)
	staging := filepath.Join(t.TempDir(), "globalmount")

	// A statically provisioned PV carries a volume handle this backend never
	// created, so Stage has to materialise it on first use.
	if _, err := b.FindByID("static-volume-1"); err != nil {
		t.Fatalf("FindByID before stage: %v", err)
	}
	if err := b.Stage("static-volume-1", staging, false); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	vol, err := b.FindByID("static-volume-1")
	if err != nil {
		t.Fatalf("FindByID after stage: %v", err)
	}
	if vol == nil {
		t.Fatal("Stage did not adopt the volume handle")
	}
}

func TestStageIsIdempotent(t *testing.T) {
	b := NewLocalDir(t.TempDir())
	staging := filepath.Join(t.TempDir(), "globalmount")

	if err := b.Stage("static-volume-1", staging, false); err != nil {
		t.Fatalf("first Stage: %v", err)
	}
	if err := b.Stage("static-volume-1", staging, false); err != nil {
		t.Fatalf("second Stage: %v", err)
	}
}

func TestMountSourceForLocalDirIsTheVolumeDirectory(t *testing.T) {
	root := t.TempDir()
	b := NewLocalDir(root)

	vol, err := b.CreateVolume("my-pvc", 1<<30)
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	source, err := b.MountSource(vol.ID, "/some/staging/path")
	if err != nil {
		t.Fatalf("MountSource: %v", err)
	}
	// The local directory backend has nothing to stage: the volume directory
	// is mounted straight into the pod, and the staging path stays unused.
	if want := filepath.Join(root, vol.ID); source != want {
		t.Errorf("MountSource = %q, want %q", source, want)
	}
}
