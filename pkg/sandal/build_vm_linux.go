//go:build linux

package sandal

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"

	sandalnet "github.com/ahmetozer/sandal/pkg/container/net"
	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/resources"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/wordgenerator"
	"strings"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kernel"
	"github.com/ahmetozer/sandal/pkg/vm/kvm"
)

// buildInKVM boots a KVM VM that runs `sandal build` with the given opts
// inside the guest, returning only after the build completes.
//
// The build context and Dockerfile are shared via VirtioFS. The image
// cache (sandal-lib) share provides a round-trip path for the output
// `.sqfs` and its sidecar: they land in the same cache directory the
// host uses for `sandal run -lw <tag>`.
func buildInKVM(opts BuildOpts) (string, error) {
	// Resolve all paths to absolute so the guest can reach them via the
	// VirtioFS /mnt/<abspath> convention.
	absCtx, err := filepath.Abs(opts.ContextDir)
	if err != nil {
		return "", fmt.Errorf("abs context: %w", err)
	}
	st, err := os.Stat(absCtx)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("context %s is not a directory", absCtx)
	}

	dockerfilePath := opts.DockerfilePath
	if dockerfilePath == "" {
		dockerfilePath = filepath.Join(absCtx, "Dockerfile")
	}
	absDockerfile, err := filepath.Abs(dockerfilePath)
	if err != nil {
		return "", fmt.Errorf("abs dockerfile: %w", err)
	}

	hostPaths := []string{absCtx}
	if !pathUnder(absDockerfile, absCtx) {
		hostPaths = append(hostPaths, absDockerfile)
	}

	// Re-assemble the build args for the guest, with paths remapped to
	// /mnt/<abspath> where the VirtioFS shares will appear.
	guestCtx := filepath.Join("/mnt", absCtx)
	guestDockerfile := filepath.Join("/mnt", absDockerfile)

	buildArgs := []string{"-t", opts.Tag}
	if opts.DockerfilePath != "" {
		buildArgs = append(buildArgs, "-f", guestDockerfile)
	}
	if opts.Push {
		buildArgs = append(buildArgs, "--push")
	}
	if opts.Target != "" {
		buildArgs = append(buildArgs, "--target", opts.Target)
	}
	for k, v := range opts.BuildArgs {
		buildArgs = append(buildArgs, "--build-arg", k+"="+v)
	}
	buildArgs = append(buildArgs, guestCtx)

	argsJSON, err := MarshalVMSubcommandArgs("build", buildArgs)
	if err != nil {
		return "", err
	}

	// VM config — reuse defaults; -cpu / -memory override.
	cfg := vmconfig.VMConfig{
		CPUCount:    vmconfig.DefaultCPUCount,
		MemoryBytes: vmconfig.DefaultMemoryMB * vmconfig.MB,
	}
	if opts.CPULimit != "" {
		if n, err := strconv.ParseFloat(opts.CPULimit, 64); err == nil && n > 0 {
			cfg.CPUCount = uint(math.Ceil(n))
		}
	}
	if opts.MemoryLimit != "" {
		if n, err := resources.ParseSize(opts.MemoryLimit); err == nil && n > 0 {
			cfg.MemoryBytes = (uint64(n) + 4095) &^ 4095
		}
	}

	kernelPath, err := kernel.EnsureKernel()
	if err != nil {
		return "", fmt.Errorf("auto-downloading kernel: %w", err)
	}
	cfg.KernelPath = kernelPath

	mounts, mountEntries, err := BuildVirtioFSMounts(hostPaths, env.LibDir)
	if err != nil {
		return "", err
	}
	cfg.Mounts = append(cfg.Mounts, mounts...)

	// Share /etc read-only for resolv.conf access during pulls inside VM.
	cfg.Mounts = append(cfg.Mounts, vmconfig.MountConfig{
		Tag:      "host-etc",
		HostPath: "/etc",
		ReadOnly: true,
	})
	mountEntries = append(mountEntries, "host-etc=/etc=/mnt/host-etc")

	// Network: single NIC on the default sandal0 bridge so the VM can
	// reach OCI registries (FROM pulls, --push).
	if _, err := sandalnet.CreateDefaultBridge(); err != nil && err != os.ErrExist {
		slog.Warn("buildInKVM", "createBridge", err)
	}
	bridgeName := sandalnet.DefaultBridgeInterface
	pid := os.Getpid()
	nlc := vmconfig.NetLinkConfig{
		Master: bridgeName,
		Ether:  []byte{0x52, 0x54, 0x00, byte(pid >> 8), byte(pid), 0x01},
	}
	cfg.NetLinks = []vmconfig.NetLinkConfig{nlc}

	// Build the same DHCP-configured Link that run-in-VM uses so the
	// guest knows to fire up DHCP on eth0. ParseFlag needs a synthetic
	// container Config to register against the bridge's IP pool.
	conts, _ := controller.Containers()
	buildName := "sandal-build-vm-" + strings.Join(wordgenerator.NameGenerate(4), "-")
	synthCfg := &config.Config{Name: buildName}
	parsedLinks, err := sandalnet.ParseFlag([]string{"ip=dhcp"}, conts, synthCfg)
	if err != nil {
		return "", fmt.Errorf("build vm net: %w", err)
	}
	netJSON, err := json.Marshal(parsedLinks)
	if err != nil {
		return "", fmt.Errorf("marshal vm net: %w", err)
	}
	vmNetEncoded := base64.StdEncoding.EncodeToString(netJSON)

	cfg.CommandLine = BuildKernelCmdLine("kvm", argsJSON, mountEntries, vmNetEncoded, nil)

	initrdPath, err := PrepareInitrd(cfg.KernelPath)
	if err != nil {
		return "", err
	}
	cfg.InitrdPath = initrdPath

	// Use the unique name generated above so parallel builds don't collide.
	vmName := buildName
	if err := vmconfig.SaveConfig(vmName, cfg); err != nil {
		return "", fmt.Errorf("saving ephemeral VM config: %w", err)
	}
	defer vmconfig.DeleteVM(vmName)

	slog.Info("buildInKVM", "action", "booting", "tag", opts.Tag, "context", absCtx)
	if err := kvm.BootWithForwards(vmName, cfg, nil, nil, nil); err != nil {
		return "", fmt.Errorf("vm boot: %w", err)
	}

	// Output path is where the guest wrote it — the image cache is shared
	// via the sandal-lib VirtioFS tag, so host and guest see the same file.
	return "", nil
}

// pathUnder reports whether child is inside parent (both must be absolute).
func pathUnder(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if len(rel) >= 2 && rel[0] == '.' && rel[1] == '.' {
		return false
	}
	return true
}
