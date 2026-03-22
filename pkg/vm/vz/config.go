package vz

import (
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
)

// Re-export shared config types and functions so existing callers
// continue to work during the migration.

const (
	DefaultCPUCount   = vmconfig.DefaultCPUCount
	DefaultMemoryMB   = vmconfig.DefaultMemoryMB
	DefaultDiskSizeMB = vmconfig.DefaultDiskSizeMB
	MB                = vmconfig.MB
)

type MountConfig = vmconfig.MountConfig
type VMConfig = vmconfig.VMConfig

var (
	VMDir        = vmconfig.VMDir
	SaveConfig   = vmconfig.SaveConfig
	LoadConfig   = vmconfig.LoadConfig
	ListVMs      = vmconfig.ListVMs
	DeleteVM     = vmconfig.DeleteVM
	PidFilePath  = vmconfig.PidFilePath
	WritePidFile = vmconfig.WritePidFile
	RemovePidFile = vmconfig.RemovePidFile
	ReadPid      = vmconfig.ReadPid
)
