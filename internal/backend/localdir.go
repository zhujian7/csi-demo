package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

const metaFileName = ".csi-demo-meta.json"

// LocalDir keeps every volume as a plain directory under Root.
type LocalDir struct {
	root string
}

func NewLocalDir(root string) *LocalDir {
	return &LocalDir{root: root}
}

func (b *LocalDir) volumePath(id string) string {
	return filepath.Join(b.root, id)
}

func (b *LocalDir) CreateVolume(name string, capacityBytes int64) (*Volume, error) {
	if existing, err := b.FindByName(name); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	if err := os.MkdirAll(b.root, 0o750); err != nil {
		return nil, fmt.Errorf("create volume root: %w", err)
	}

	vol := &Volume{
		ID:            uuid.NewString(),
		Name:          name,
		CapacityBytes: capacityBytes,
		CreatedAt:     time.Now().UTC(),
	}
	dir := b.volumePath(vol.ID)
	if err := os.Mkdir(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create volume %s: %w", vol.ID, err)
	}
	if err := writeMeta(dir, vol); err != nil {
		return nil, err
	}
	return vol, nil
}

func (b *LocalDir) DeleteVolume(id string) error {
	if err := os.RemoveAll(b.volumePath(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete volume %s: %w", id, err)
	}
	return nil
}

func (b *LocalDir) FindByID(id string) (*Volume, error) {
	return readMeta(b.volumePath(id))
}

// FindByName walks the volume root. A demo-sized backend can afford a linear
// scan; a real driver would keep an index or derive the id from the name.
func (b *LocalDir) FindByName(name string) (*Volume, error) {
	entries, err := os.ReadDir(b.root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read volume root: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		vol, err := readMeta(filepath.Join(b.root, entry.Name()))
		if err != nil {
			return nil, err
		}
		if vol != nil && vol.Name == name {
			return vol, nil
		}
	}
	return nil, nil
}

// Stage prepares the volume on this node. For a plain directory that means
// adopting the volume when static provisioning left no record of it, and
// creating the staging path. There is no device to format or mount, so the
// staging path ends up unused — Task 3 replaces this backend with one where
// staging does real work.
func (b *LocalDir) Stage(id, stagingPath string, _ bool) error {
	vol, err := b.FindByID(id)
	if err != nil {
		return err
	}
	if vol == nil {
		if err := b.adopt(id); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(stagingPath, 0o750); err != nil {
		return fmt.Errorf("create staging path %s: %w", stagingPath, err)
	}
	return nil
}

// adopt records a volume this backend did not create, which is what static
// provisioning looks like from the node's point of view.
func (b *LocalDir) adopt(id string) error {
	if err := os.MkdirAll(b.root, 0o750); err != nil {
		return fmt.Errorf("create volume root: %w", err)
	}
	dir := b.volumePath(id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("adopt volume %s: %w", id, err)
	}
	return writeMeta(dir, &Volume{
		ID:        id,
		Name:      id,
		CreatedAt: time.Now().UTC(),
	})
}

func (b *LocalDir) Unstage(_, stagingPath string) error {
	if err := os.RemoveAll(stagingPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove staging path %s: %w", stagingPath, err)
	}
	return nil
}

func (b *LocalDir) MountSource(id, _ string) (string, error) {
	return b.volumePath(id), nil
}

func writeMeta(dir string, vol *Volume) error {
	data, err := json.Marshal(vol)
	if err != nil {
		return fmt.Errorf("encode volume metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, metaFileName), data, 0o640); err != nil {
		return fmt.Errorf("write volume metadata: %w", err)
	}
	return nil
}

// readMeta returns nil (and no error) when the directory carries no metadata,
// so callers can treat "absent" and "not ours" the same way.
func readMeta(dir string) (*Volume, error) {
	data, err := os.ReadFile(filepath.Join(dir, metaFileName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read volume metadata in %s: %w", dir, err)
	}
	var vol Volume
	if err := json.Unmarshal(data, &vol); err != nil {
		return nil, fmt.Errorf("decode volume metadata in %s: %w", dir, err)
	}
	return &vol, nil
}
