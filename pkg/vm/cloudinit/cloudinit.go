package cloudinit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GenerateNoCloudISO creates a NoCloud datasource ISO for cloud-init/tiny-cloud.
// Returns the path to the generated ISO (caller must remove it).
func GenerateNoCloudISO(mounts []MountInfo) (string, error) {
	if len(mounts) == 0 {
		return "", nil
	}

	// Create temp directory for cloud-init files
	tmpDir, err := os.MkdirTemp("", "sandal-cloudinit-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	// Write meta-data (required, can be minimal)
	metaData := `instance-id: sandal-vm-01
local-hostname: sandal-vm
`
	if err := os.WriteFile(filepath.Join(tmpDir, "meta-data"), []byte(metaData), 0644); err != nil {
		return "", err
	}

	// Write user-data with runcmd to mount virtiofs shares
	userData := generateUserData(mounts)
	if err := os.WriteFile(filepath.Join(tmpDir, "user-data"), []byte(userData), 0644); err != nil {
		return "", err
	}

	// Generate ISO with label CIDATA (recognized by tiny-cloud)
	isoPath := filepath.Join(os.TempDir(), fmt.Sprintf("sandal-cloudinit-%d.iso", os.Getpid()))

	// Try genisoimage first, fall back to mkisofs
	var cmd *exec.Cmd
	if _, err := exec.LookPath("genisoimage"); err == nil {
		cmd = exec.Command("genisoimage",
			"-output", isoPath,
			"-volid", "CIDATA",
			"-joliet",
			"-rock",
			tmpDir,
		)
	} else if _, err := exec.LookPath("mkisofs"); err == nil {
		cmd = exec.Command("mkisofs",
			"-output", isoPath,
			"-volid", "CIDATA",
			"-joliet",
			"-rock",
			tmpDir,
		)
	} else {
		return "", fmt.Errorf("genisoimage or mkisofs not found (install via: brew install cdrtools)")
	}

	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to create ISO: %w\n%s", err, output)
	}

	return isoPath, nil
}

type MountInfo struct {
	Tag      string
	ReadOnly bool
}

func generateUserData(mounts []MountInfo) string {
	var buf strings.Builder
	buf.WriteString("#cloud-config\n")
	buf.WriteString("# sandal-vm auto-generated cloud-init config\n\n")

	// Load modules at boot
	buf.WriteString("bootcmd:\n")
	buf.WriteString("  - modprobe fuse\n")
	buf.WriteString("  - modprobe virtiofs\n\n")

	// Mount virtiofs shares
	buf.WriteString("runcmd:\n")
	for _, m := range mounts {
		opts := "rw"
		if m.ReadOnly {
			opts = "ro"
		}
		buf.WriteString(fmt.Sprintf("  - mkdir -p /mnt/%s\n", m.Tag))
		buf.WriteString(fmt.Sprintf("  - mount -t virtiofs -o %s %s /mnt/%s || true\n", opts, m.Tag, m.Tag))
	}

	return buf.String()
}
