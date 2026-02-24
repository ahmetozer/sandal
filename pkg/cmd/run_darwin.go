//go:build darwin

package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/vm/initrd"
	"github.com/ahmetozer/sandal/pkg/vm/vz"
)

func Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no command option provided")
	}

	thisFlags, contArgs, splitErr := SplitFlagsArgs(args)

	f := flag.NewFlagSet("run", flag.ExitOnError)

	var (
		vmFlag  string
		name    string
		rm      bool
		lw      repeatableFlag
		volumes repeatableFlag
	)

	f.StringVar(&vmFlag, "vm", "default-vm", "VM config name")
	f.StringVar(&name, "name", "", "name of the container")
	f.BoolVar(&rm, "rm", true, "remove container files on exit")
	f.Var(&lw, "lw", "Lower directory of the root file system")
	f.Var(&volumes, "v", "volume mount point")

	// Accept Linux-only flags without error (ignored on macOS)
	var ignored string
	var ignoredBool bool
	var ignoredUint uint
	f.BoolVar(&ignoredBool, "d", false, "")
	f.BoolVar(&ignoredBool, "ro", false, "")
	f.BoolVar(&ignoredBool, "startup", false, "")
	f.BoolVar(&ignoredBool, "env-all", false, "")
	f.StringVar(&ignored, "dir", "", "")
	f.StringVar(&ignored, "resolv", "", "")
	f.StringVar(&ignored, "hosts", "", "")
	f.StringVar(&ignored, "chdir", "", "")
	f.StringVar(&ignored, "rdir", "", "")
	f.StringVar(&ignored, "devtmpfs", "", "")
	f.StringVar(&ignored, "user", "", "")
	f.StringVar(&ignored, "memory", "", "")
	f.StringVar(&ignored, "cpu", "", "")
	f.UintVar(&ignoredUint, "tmp", 0, "")
	var ignoredRepeat repeatableFlag
	f.Var(&ignoredRepeat, "net", "")
	f.Var(&ignoredRepeat, "env-pass", "")
	f.Var(&ignoredRepeat, "rcp", "")
	f.Var(&ignoredRepeat, "rci", "")
	f.Var(&ignoredRepeat, "cap-add", "")
	f.Var(&ignoredRepeat, "cap-drop", "")
	// Namespace flags
	f.StringVar(&ignored, "ns-mount", "", "")
	f.StringVar(&ignored, "ns-ipc", "", "")
	f.StringVar(&ignored, "ns-pid", "", "")
	f.StringVar(&ignored, "ns-net", "", "")
	f.StringVar(&ignored, "ns-user", "", "")
	f.StringVar(&ignored, "ns-uts", "", "")
	f.StringVar(&ignored, "ns-cgroup", "", "")

	if err := f.Parse(thisFlags); err != nil {
		return fmt.Errorf("error parsing flags: %v", err)
	}

	if splitErr != nil {
		return splitErr
	}

	if len(lw) == 0 {
		return fmt.Errorf("at least one -lw (lower directory) is required")
	}

	// Load VM config (when -vm=new, use default-vm as the base)
	configName := vmFlag
	if vmFlag == "new" {
		configName = "default-vm"
	}
	cfg, err := vz.LoadConfig(configName)
	if err != nil {
		return fmt.Errorf("loading VM config '%s': %w (create with: sandal vm create -name %s -kernel <path>)", configName, err, configName)
	}

	// Resolve Linux sandal binary
	sandalBin := os.Getenv("SANDAL_VM_BIN")
	if sandalBin == "" {
		home, _ := os.UserHomeDir()
		sandalBin = filepath.Join(home, ".sandal-vm", "bin", "sandal")
	}
	if _, err := os.Stat(sandalBin); err != nil {
		return fmt.Errorf("Linux sandal binary not found at %s (cross-compile with: GOOS=linux CGO_ENABLED=0 go build -o %s .)", sandalBin, sandalBin)
	}

	// Build VirtioFS mounts and adjusted args for inside the VM
	var vmMounts []vz.MountConfig
	var mountTags []string
	var vmArgs []string

	vmArgs = append(vmArgs, "run")
	if rm {
		vmArgs = append(vmArgs, "-rm")
	}
	if name != "" {
		vmArgs = append(vmArgs, "-name", name)
	}
	/*
		TODO Instead of append, set default runs at top
		! With below approach, it will break user inputs
	*/
	// Use host namespaces where possible inside the VM
	vmArgs = append(vmArgs, "-ns-net", "host", "-ns-cgroup", "host", "-ns-user", "host")
	// Skip resolv.conf/hosts copying (no /etc in initramfs)
	vmArgs = append(vmArgs, "-resolv", "image", "-hosts", "image")

	// Process -lw flags: mount each host path via VirtioFS, adjust to /mnt/lw-N
	for i, lwPath := range lw {
		absPath, err := filepath.Abs(lwPath)
		if err != nil {
			return fmt.Errorf("resolving -lw path %q: %w", lwPath, err)
		}
		tag := fmt.Sprintf("lw-%d", i)
		vmMounts = append(vmMounts, vz.MountConfig{
			Tag:      tag,
			HostPath: absPath,
		})
		mountTags = append(mountTags, tag)
		vmArgs = append(vmArgs, "-lw", fmt.Sprintf("/mnt/%s", tag))
	}

	// Process -v flags: mount host path via VirtioFS, adjust to /mnt/vol-N:container
	for i, vol := range volumes {
		parts := strings.SplitN(vol, ":", 2)
		if len(parts) < 2 {
			return fmt.Errorf("invalid volume format %q (expected host:container)", vol)
		}
		hostPath := parts[0]
		containerPath := parts[1]

		absPath, err := filepath.Abs(hostPath)
		if err != nil {
			return fmt.Errorf("resolving -v host path %q: %w", hostPath, err)
		}
		tag := fmt.Sprintf("vol-%d", i)
		vmMounts = append(vmMounts, vz.MountConfig{
			Tag:      tag,
			HostPath: absPath,
		})
		mountTags = append(mountTags, tag)
		vmArgs = append(vmArgs, "-v", fmt.Sprintf("/mnt/%s:%s", tag, containerPath))
	}

	// Add -- and container command
	vmArgs = append(vmArgs, "--")
	vmArgs = append(vmArgs, contArgs...)

	// Build compact JSON (no spaces) for kernel cmdline env var
	argsJSON, err := json.Marshal(vmArgs)
	if err != nil {
		return fmt.Errorf("marshaling VM args: %w", err)
	}

	// Append mounts to VM config
	cfg.Mounts = append(cfg.Mounts, vmMounts...)

	// Build kernel command line with SANDAL_VM_ARGS and SANDAL_VM_MOUNTS
	// sandal as PID 1 reads these env vars to mount virtiofs and dispatch run
	cfg.CommandLine = fmt.Sprintf("console=hvc0 SANDAL_VM_ARGS=%s SANDAL_VM_MOUNTS=%s",
		string(argsJSON), strings.Join(mountTags, ","))

	// Create initrd: sandal Linux binary as /init, prepend base initrd if available
	// (base initrd provides kernel modules like virtiofs)
	initrdPath, err := initrd.CreateFromBinary(sandalBin, cfg.InitrdPath)
	if err != nil {
		return fmt.Errorf("creating initrd from sandal binary: %w", err)
	}
	defer os.Remove(initrdPath)
	cfg.InitrdPath = initrdPath

	// Use the VM config name for identification (PID file, list, stop/kill).
	// Only create an ephemeral VM when -vm=new.
	vmName := vmFlag
	if vmFlag == "new" {
		vmName = fmt.Sprintf("run-%d", os.Getpid())
		if err := vz.SaveConfig(vmName, cfg); err != nil {
			return fmt.Errorf("saving ephemeral VM config: %w", err)
		}
		defer vz.DeleteVM(vmName)
	}

	return bootVM(vmName, cfg)
}
