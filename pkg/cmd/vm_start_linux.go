//go:build linux

package cmd

import (
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kvm"
)

func platformBoot(name string, cfg vmconfig.VMConfig) error {
	return kvm.Boot(name, cfg)
}
