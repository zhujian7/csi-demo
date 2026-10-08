// Package mountutil wraps the mount calls shared by the CSI driver and the
// volume backends.
package mountutil

import (
	"os"

	"k8s.io/mount-utils"
	utilexec "k8s.io/utils/exec"
)

var mounter = mount.New("")

// IsMountPoint reports whether path is currently a mount point. A path that
// does not exist is not one.
func IsMountPoint(path string) (bool, error) {
	notMounted, err := mounter.IsLikelyNotMountPoint(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !notMounted, nil
}

// BindMount makes source visible at target, creating target when needed, and
// does nothing when target is already mounted. kubelet retries publish calls,
// and stacking a second mount on the same target would be wrong.
func BindMount(source, target string, readonly bool) error {
	mounted, err := IsMountPoint(target)
	if err != nil {
		return err
	}
	if mounted {
		return nil
	}
	if err := os.MkdirAll(target, 0o750); err != nil {
		return err
	}

	options := []string{"bind"}
	if readonly {
		options = append(options, "ro")
	}
	return mounter.Mount(source, target, "", options)
}

// Unmount detaches target. An absent or already unmounted target is not an error.
func Unmount(target string) error {
	mounted, err := IsMountPoint(target)
	if err != nil {
		return err
	}
	if !mounted {
		return nil
	}
	return mounter.Unmount(target)
}

// FormatAndMount returns a helper that formats a device when it carries no
// filesystem and then mounts it.
func FormatAndMount() *mount.SafeFormatAndMount {
	return &mount.SafeFormatAndMount{Interface: mounter, Exec: utilexec.New()}
}
