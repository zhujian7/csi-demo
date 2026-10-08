package backend

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zhujian7/csi-demo/internal/mountutil"
)

func TestLoopDeviceFromLosetupQuery(t *testing.T) {
	cases := map[string]string{
		"/dev/loop0: []: (/var/lib/csi-demo/vol-1/backing.img)": "/dev/loop0",
		"": "",
	}
	for input, want := range cases {
		if got := loopDeviceFromLosetupQuery(input); got != want {
			t.Errorf("loopDeviceFromLosetupQuery(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEnsureBackingFileCreatesSparseFileOfRequestedSize(t *testing.T) {
	b := NewLoopDev(t.TempDir())
	path := filepath.Join(t.TempDir(), "backing.img")

	if err := b.ensureBackingFile(path, 1<<20); err != nil {
		t.Fatalf("ensureBackingFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat backing file: %v", err)
	}
	if got := info.Size(); got != 1<<20 {
		t.Errorf("backing file size = %d, want %d", got, 1<<20)
	}
}

func TestEnsureBackingFileKeepsExistingFile(t *testing.T) {
	b := NewLoopDev(t.TempDir())
	path := filepath.Join(t.TempDir(), "backing.img")

	if err := b.ensureBackingFile(path, 1<<20); err != nil {
		t.Fatalf("first ensureBackingFile: %v", err)
	}
	// A second call must not truncate an already populated volume.
	if err := b.ensureBackingFile(path, 1<<30); err != nil {
		t.Fatalf("second ensureBackingFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat backing file: %v", err)
	}
	if got := info.Size(); got != 1<<20 {
		t.Errorf("backing file was resized to %d, want it left at %d", got, 1<<20)
	}
}

func TestMountSourceForLoopBackendIsTheStagingPath(t *testing.T) {
	b := NewLoopDev(t.TempDir())
	staging := "/var/lib/kubelet/plugins/kubernetes.io/csi/csi.demo.cubestack.io/abc/globalmount"

	got, err := b.MountSource("vol-1", staging)
	if err != nil {
		t.Fatalf("MountSource: %v", err)
	}
	// Unlike the directory backend, the device is mounted once at the staging
	// path and every pod binds from there.
	if got != staging {
		t.Errorf("MountSource = %q, want %q", got, staging)
	}
}

// TestStageFormatsAndMounts needs root: it attaches a loop device and runs
// mkfs. Run it in the container or with sudo.
func TestStageFormatsAndMounts(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to attach a loop device and format a filesystem")
	}
	b := NewLoopDev(t.TempDir())
	staging := filepath.Join(t.TempDir(), "globalmount")

	vol, err := b.CreateVolume("my-pvc", 64<<20)
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if err := b.Stage(vol.ID, staging, false); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	t.Cleanup(func() { _ = b.Unstage(vol.ID, staging) })

	mounted, err := mountutil.IsMountPoint(staging)
	if err != nil {
		t.Fatalf("inspect staging path: %v", err)
	}
	if !mounted {
		t.Error("staging path is not a mount point after Stage")
	}
}
