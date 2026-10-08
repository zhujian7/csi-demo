package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/mount-utils"
	utilexec "k8s.io/utils/exec"

	"github.com/zhujian7/csi-demo/internal/mountutil"
)

const (
	backingFileName = "backing.img"
	// defaultVolumeBytes is used when a volume arrives without a capacity,
	// which is what static provisioning looks like.
	defaultVolumeBytes = 1 << 30
	fsType             = "ext4"
)

// LoopDev serves each volume as a real block device backed by a sparse file.
// That is what gives Stage actual work: format a filesystem and mount it once
// at the staging path, so every pod on the node binds from that single mount.
type LoopDev struct {
	*LocalDir

	mounter *mount.SafeFormatAndMount
	exec    utilexec.Interface
}

func NewLoopDev(root string) *LoopDev {
	return &LoopDev{
		LocalDir: NewLocalDir(root),
		mounter:  mountutil.FormatAndMount(),
		exec:     utilexec.New(),
	}
}

func (b *LoopDev) backingPath(id string) string {
	return filepath.Join(b.volumePath(id), backingFileName)
}

// MountSource returns the staging path: the device is mounted there, not in
// the volume directory.
func (b *LoopDev) MountSource(id, stagingPath string) (string, error) {
	if _, err := b.FindByID(id); err != nil {
		return "", err
	}
	return stagingPath, nil
}

// Stage attaches the backing file as a loop device, puts a filesystem on it
// and mounts it at stagingPath.
//
// Idempotent by checking the live mount table first: kubelet retries, and a
// second mkfs would destroy the filesystem the first call created.
func (b *LoopDev) Stage(id, stagingPath string, readonly bool) error {
	mounted, err := mountutil.IsMountPoint(stagingPath)
	if err != nil {
		return fmt.Errorf("inspect staging path %s: %w", stagingPath, err)
	}
	if mounted {
		return nil
	}

	vol, err := b.FindByID(id)
	if err != nil {
		return err
	}
	if vol == nil {
		if err := b.adopt(id); err != nil {
			return err
		}
		if vol, err = b.FindByID(id); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(stagingPath, 0o750); err != nil {
		return fmt.Errorf("create staging path %s: %w", stagingPath, err)
	}

	backing := b.backingPath(id)
	if err := b.ensureBackingFile(backing, vol.CapacityBytes); err != nil {
		return err
	}
	device, err := b.attachLoop(backing)
	if err != nil {
		return err
	}

	if readonly {
		return b.mounter.Mount(device, stagingPath, fsType, []string{"ro"})
	}
	return b.mounter.FormatAndMount(device, stagingPath, fsType, nil)
}

// Unstage unpacks Stage in reverse: unmount first, then release the loop
// device. Detaching while mounted would lose the filesystem.
func (b *LoopDev) Unstage(id, stagingPath string) error {
	if err := mountutil.Unmount(stagingPath); err != nil {
		return fmt.Errorf("unmount staging path %s: %w", stagingPath, err)
	}
	if err := os.RemoveAll(stagingPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove staging path %s: %w", stagingPath, err)
	}
	return b.detachLoop(b.backingPath(id))
}

// ensureBackingFile creates a sparse file of the requested size. Sparse means
// a 100 GiB volume costs almost no disk until it is written to.
func (b *LoopDev) ensureBackingFile(path string, sizeBytes int64) error {
	if sizeBytes <= 0 {
		sizeBytes = defaultVolumeBytes
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat backing file %s: %w", path, err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("create backing file %s: %w", path, err)
	}
	defer file.Close()
	if err := file.Truncate(sizeBytes); err != nil {
		return fmt.Errorf("size backing file %s: %w", path, err)
	}
	return nil
}

// attachLoop returns the loop device backing file, reusing an existing one.
// Attaching a second device to the same file would let two filesystems be
// written over each other.
func (b *LoopDev) attachLoop(backing string) (string, error) {
	out, _ := b.exec.Command("losetup", "-j", backing).CombinedOutput()
	if device := loopDeviceFromLosetupQuery(string(out)); device != "" {
		return device, nil
	}

	out, err := b.exec.Command("losetup", "--find", "--show", backing).Output()
	if err != nil {
		return "", fmt.Errorf("attach %s as a loop device: %w", backing, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (b *LoopDev) detachLoop(backing string) error {
	out, _ := b.exec.Command("losetup", "-j", backing).CombinedOutput()
	device := loopDeviceFromLosetupQuery(string(out))
	if device == "" {
		return nil
	}
	if _, err := b.exec.Command("losetup", "-d", device).CombinedOutput(); err != nil {
		return fmt.Errorf("detach loop device %s: %w", device, err)
	}
	return nil
}

// loopDeviceFromLosetupQuery reads the device out of `losetup -j` output,
// which looks like "/dev/loop0: []: (/path/to/backing.img)".
func loopDeviceFromLosetupQuery(out string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	device, _, _ := strings.Cut(line, ":")
	return strings.TrimSpace(device)
}
