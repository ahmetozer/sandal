//go:build linux

package overlayfs

import (
	"fmt"
	"log/slog"
	"os"

	detectfs "github.com/ahmetozer/sandal/pkg/lib/detectFs"
	"github.com/ahmetozer/sandal/pkg/lib/loopdev"
	"github.com/ahmetozer/sandal/pkg/lib/mkfs"
	"github.com/ahmetozer/sandal/pkg/vm/disk"
	"golang.org/x/sys/unix"
)

const defaultChangeDirImageSize = 4 * 1024 * 1024 * 1024 // 4GB sparse

// VMChangeMount holds state needed to clean up a VM change dir
// (unmount ext4 + detach loop device).
type VMChangeMount struct {
	ImagePath  string
	MountPoint string
	LoopDev    loopdev.Config
}

// vmChangeMounts tracks active VM change dir mounts for cleanup.
var vmChangeMounts = map[string]*VMChangeMount{}

func RegisterVMChangeMount(changeDir string, mount *VMChangeMount) {
	vmChangeMounts[changeDir] = mount
}

func GetVMChangeMount(changeDir string) *VMChangeMount {
	return vmChangeMounts[changeDir]
}

func UnregisterVMChangeMount(changeDir string) {
	delete(vmChangeMounts, changeDir)
}

// prepareVMChangeDir creates a sparse ext4 disk image on VirtioFS,
// loop-mounts it, and returns the mount state for later cleanup.
// If the image already exists with an ext4 filesystem, it reuses it
// (preserving container changes across restarts).
func prepareVMChangeDir(changeDir string) (*VMChangeMount, error) {
	imagePath := changeDir + ".img"

	// Create sparse image if it doesn't exist
	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		if err := os.MkdirAll(changeDir, 0755); err != nil {
			return nil, fmt.Errorf("creating change dir parent: %w", err)
		}
		if err := disk.CreateRawDisk(imagePath, defaultChangeDirImageSize); err != nil {
			return nil, fmt.Errorf("creating change dir image: %w", err)
		}
		slog.Debug("prepareVMChangeDir", slog.String("action", "created-image"), slog.String("path", imagePath))
	}

	// Attach to loop device (read-write)
	lc, err := loopdev.FindFreeLoopDevice()
	if err != nil {
		return nil, fmt.Errorf("finding free loop device: %w", err)
	}
	lc.RW = true
	if err := lc.Attach(imagePath); err != nil {
		return nil, fmt.Errorf("attaching loop device: %w", err)
	}

	// Check if already formatted (container restart case)
	needsFormat := true
	if fsType, err := detectfs.DetectFilesystem(lc.Path); err == nil && fsType == "ext4" {
		needsFormat = false
		slog.Debug("prepareVMChangeDir", slog.String("action", "reusing-ext4"), slog.String("loopDev", lc.Path))
	}

	if needsFormat {
		slog.Debug("prepareVMChangeDir", slog.String("action", "formatting"), slog.String("loopDev", lc.Path))
		if err := mkfs.FormatExt4(lc.Path); err != nil {
			lc.Detach()
			return nil, fmt.Errorf("formatting ext4 %s: %w", lc.Path, err)
		}
	}

	// Mount at the change dir path
	if err := os.MkdirAll(changeDir, 0755); err != nil {
		lc.Detach()
		return nil, fmt.Errorf("creating mount point: %w", err)
	}
	if err := unix.Mount(lc.Path, changeDir, "ext4", 0, ""); err != nil {
		lc.Detach()
		return nil, fmt.Errorf("mounting ext4 at %s: %w", changeDir, err)
	}

	return &VMChangeMount{
		ImagePath:  imagePath,
		MountPoint: changeDir,
		LoopDev:    lc,
	}, nil
}

// Cleanup unmounts the ext4 filesystem and detaches the loop device.
func (m *VMChangeMount) Cleanup() error {
	var firstErr error
	if err := unix.Unmount(m.MountPoint, 0); err != nil {
		if !os.IsNotExist(err) {
			firstErr = fmt.Errorf("unmount %s: %w", m.MountPoint, err)
		}
	}
	if err := m.LoopDev.Detach(); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("detach loop %s: %w", m.LoopDev.Path, err)
		}
	}
	return firstErr
}
