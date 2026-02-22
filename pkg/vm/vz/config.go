package vz

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	DefaultCPUCount   = 2
	DefaultMemoryMB   = 512
	DefaultDiskSizeMB = 4096
	MB                = 1024 * 1024
)

type MountConfig struct {
	Tag      string `json:"tag"`
	HostPath string `json:"host_path"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

type VMConfig struct {
	KernelPath  string        `json:"kernel_path"`
	InitrdPath  string        `json:"initrd_path,omitempty"`
	CommandLine string        `json:"command_line"`
	DiskPath    string        `json:"disk_path"`
	ISOPath     string        `json:"iso_path,omitempty"`
	Mounts      []MountConfig `json:"mounts,omitempty"`
	CPUCount    uint          `json:"cpu_count"`
	MemoryBytes uint64        `json:"memory_bytes"`
}

func (c *VMConfig) Validate() error {
	if c.KernelPath == "" {
		return fmt.Errorf("kernel_path is required")
	}
	if _, err := os.Stat(c.KernelPath); err != nil {
		return fmt.Errorf("kernel not found: %s", c.KernelPath)
	}
	if c.InitrdPath != "" {
		if _, err := os.Stat(c.InitrdPath); err != nil {
			return fmt.Errorf("initrd not found: %s", c.InitrdPath)
		}
	}
	if c.DiskPath != "" {
		if _, err := os.Stat(c.DiskPath); err != nil {
			return fmt.Errorf("disk image not found: %s", c.DiskPath)
		}
	}
	if c.ISOPath != "" {
		if _, err := os.Stat(c.ISOPath); err != nil {
			return fmt.Errorf("ISO image not found: %s", c.ISOPath)
		}
	}
	for i, m := range c.Mounts {
		if m.Tag == "" {
			return fmt.Errorf("mount[%d]: tag is required", i)
		}
		info, err := os.Stat(m.HostPath)
		if err != nil {
			return fmt.Errorf("mount[%d]: host path not found: %s", i, m.HostPath)
		}
		if !info.IsDir() {
			return fmt.Errorf("mount[%d]: host path is not a directory: %s", i, m.HostPath)
		}
	}
	if c.CPUCount == 0 {
		return fmt.Errorf("cpu_count must be at least 1")
	}
	if c.MemoryBytes == 0 {
		return fmt.Errorf("memory_bytes must be set")
	}
	if c.MemoryBytes%(1024*1024) != 0 {
		return fmt.Errorf("memory must be a multiple of 1MB")
	}
	return nil
}

func VMDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sandal-vm", "machines")
}

func SaveConfig(name string, cfg VMConfig) error {
	dir := filepath.Join(VMDir(), name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)
}

func LoadConfig(name string) (VMConfig, error) {
	var cfg VMConfig
	data, err := os.ReadFile(filepath.Join(VMDir(), name, "config.json"))
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(data, &cfg)
	return cfg, err
}

func ListVMs() ([]string, error) {
	dir := VMDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func DeleteVM(name string) error {
	return os.RemoveAll(filepath.Join(VMDir(), name))
}

func PidFilePath(name string) string {
	return filepath.Join(VMDir(), name, "pid")
}

func WritePidFile(name string) error {
	return os.WriteFile(PidFilePath(name), []byte(strconv.Itoa(os.Getpid())), 0644)
}

func RemovePidFile(name string) {
	os.Remove(PidFilePath(name))
}

// ReadPid reads the PID from the pidfile and checks if the process is alive.
// Returns the PID and true if the process exists, or 0 and false otherwise.
func ReadPid(name string) (int, bool) {
	data, err := os.ReadFile(PidFilePath(name))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	// Signal 0 checks if process exists without sending a signal
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return 0, false
	}
	return pid, true
}
