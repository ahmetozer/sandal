//go:build darwin

package cmd

import (
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/vz"
)

func platformBoot(name string, cfg vmconfig.VMConfig) error {
	return vz.Boot(name, cfg)
}
