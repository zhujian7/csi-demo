// Package backend decides where a volume's bytes live and how they are made
// visible on the node. It knows nothing about CSI.
package backend

import "time"

// Volume is the driver's record of one volume.
type Volume struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	CapacityBytes int64     `json:"capacityBytes"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Backend stores volumes on the node's local disk.
//
// Every method must be safe to call again with the same arguments: kubelet
// and the CSI sidecars retry, and a retry that creates a second volume or a
// second mount is a bug, not a duplicate.
type Backend interface {
	// CreateVolume materialises a new volume, or returns the existing one when
	// a volume with the same name is already present.
	CreateVolume(name string, capacityBytes int64) (*Volume, error)

	// DeleteVolume removes a volume and its data. An unknown id is not an error.
	DeleteVolume(id string) error

	// FindByID returns the volume with this id, or nil when it does not exist.
	FindByID(id string) (*Volume, error)

	// FindByName returns the volume created under this name, or nil.
	FindByName(name string) (*Volume, error)

	// Stage makes the volume's data ready on this node at stagingPath.
	Stage(id, stagingPath string, readonly bool) error

	// Unstage releases what Stage prepared. An already unstaged volume is not
	// an error.
	Unstage(id, stagingPath string) error

	// MountSource returns the path NodePublishVolume should bind-mount into the
	// pod. It differs per backend: a plain directory is mounted where it lies,
	// while a device volume must be mounted from the staging path.
	MountSource(id, stagingPath string) (string, error)
}
