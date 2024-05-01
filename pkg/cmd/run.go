package cmd

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/container"
	"github.com/ahmetozer/sandal/pkg/net"
)

func run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no command option provided")
	}

	thisFlags, args := SplitArgs(args)
	c := config.NewContainer()
	f := flag.NewFlagSet("run", flag.ExitOnError)

	var (
		help bool
		err  error
	)
	f.BoolVar(&help, "help", false, "show help")
	f.StringVar(&c.Name, "name", config.GenerateContainerId(), "name of the container")
	f.StringVar(&c.SquashfsFile, "sq", "./rootfs.squasfs", "squashfs image location")
	f.StringVar(&c.RootfsDir, "rootfs", defaultRootfs(&c), "rootfs directory")

	f.UintVar(&c.TmpSize, "tmp-size", 0, "allocate changes at memory instead of disk. 0 means disk is used and other values unit is in MB")

	HostIface := config.NetIface{ALocFor: config.ALocForHost}
	f.StringVar(&HostIface.Type, "net-type", "bridge", "bridge or macvlan")
	f.StringVar(&HostIface.Name, "host-net", "sandal0", "host interface for bridge or macvlan")
	f.StringVar(&HostIface.IP, "host-ips", "172.16.0.1/24;fd34:0135:0123::1/64", "host interface ips")

	PodIface := config.NetIface{Type: "veth", Main: []config.NetIface{HostIface}}
	// f.StringVar(&PodIface.Name, "pod-net", "eth0", "container interface name")
	f.StringVar(&PodIface.IP, "pod-ips", "172.16.0.2/24;fd34:0135:0123::2/64", "container interface ips")

	f.StringVar(&c.NS.Net, "net", "", "net namespace or host")
	f.StringVar(&c.NS.Pid, "pid", "", "pid namespace or host")
	f.StringVar(&c.NS.Uts, "uts", "", "uts namespace or host")
	f.StringVar(&c.NS.User, "user", "", "user namespace or host")

	if err := f.Parse(thisFlags); err != nil {
		return fmt.Errorf("error parsing flags: %v", err)
	}

	if help {
		f.Usage()
		return nil
	}

	if c.NS.Net != "host" {
		defer net.Clear(&c)
		err = net.CreateIface(&c, &HostIface)
		if err != nil {
			return fmt.Errorf("error creating host interface: %v", err)
		}
		err = net.CreateIface(&c, &PodIface)
		if err != nil {
			return fmt.Errorf("error creating pod interface: %v", err)
		}
	}

	defer func() {
		if err := os.RemoveAll(c.ContDir()); err != nil {
			slog.Info("removeall: %v", err)
		}
	}()

	// mount squasfs
	err = container.MountRootfs(&c)
	defer func() {
		if err := container.UmountRootfs(&c); err != nil {
			slog.Info("umount: %v", err)
		}
	}()
	if err != nil {
		return fmt.Errorf("error mount: %v", err)
	}
	if !hasItExecutable(args) {
		return fmt.Errorf("no executable provided")
	}

	// Starting proccess
	return container.Start(c, args)
}

func defaultRootfs(c *config.Config) string {
	return path.Join(config.Workdir, "container", c.Name, "rootfs")
}
