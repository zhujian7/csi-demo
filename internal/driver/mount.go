package driver

import (
	"os"

	"k8s.io/mount-utils"
)

var mounter = mount.New("")

// bindMount makes source visible at target, creating target when needed.
//
// It is deliberately idempotent: kubelet retries NodePublishVolume, and a
// second bind mount of the same pair would stack mounts. The check is against
// the live mount table, not in-memory state, so it also holds after a restart.
func bindMount(source, target string, readonly bool) error {
	notMounted, err := mounter.IsLikelyNotMountPoint(target)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(target, 0o750); err != nil {
			return err
		}
		notMounted = true
	}
	if !notMounted {
		return nil
	}

	options := []string{"bind"}
	if readonly {
		options = append(options, "ro")
	}
	return mounter.Mount(source, target, "", options)
}

// unmount detaches target. An absent or already unmounted target is not an
// error, which keeps a retried NodeUnpublishVolume harmless.
func unmount(target string) error {
	notMounted, err := mounter.IsLikelyNotMountPoint(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if notMounted {
		return nil
	}
	return mounter.Unmount(target)
}
