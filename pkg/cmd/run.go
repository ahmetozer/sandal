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
	c.HostArgs = os.Args
	f := flag.NewFlagSet("run", flag.ExitOnError)

	var (
		help bool
		err  error
	)

	f.BoolVar(&help, "help", false, "show this help message")
	f.BoolVar(&c.Background, "d", false, "run container in background")
	f.StringVar(&c.Name, "name", config.GenerateContainerId(), "name of the container")
	f.StringVar(&c.SquashfsFile, "sq", "./rootfs.sqfs", "squashfs image location")
	// f.StringVar(&c.RootfsDir, "rootfs", "", "rootfs directory")
	f.BoolVar(&c.ReadOnly, "ro", false, "read only rootfs")

	f.BoolVar(&c.Keep, "keep", false, "do not remove container files on exit")

	f.BoolVar(&c.EnvAll, "env-all", false, "send all enviroment variables to container")

	f.UintVar(&c.TmpSize, "tmp", 0, "allocate changes at memory instead of disk. unit is in MB, disk is used used by default")

	HostIface := config.NetIface{ALocFor: config.ALocForHost}
	f.StringVar(&HostIface.Type, "net-type", "bridge", "bridge, macvlan, ipvlan")
	f.StringVar(&HostIface.Name, "host-net", "sandal0", "host interface for bridge or macvlan")
	f.StringVar(&HostIface.IP, "host-ips", "172.16.0.1/24;fd34:0135:0123::1/64", "host interface ips")

	PodIface := config.NetIface{Type: "veth"}
	// f.StringVar(&PodIface.Name, "pod-net", "eth0", "container interface name")
	f.StringVar(&PodIface.IP, "pod-ips", "", "container interface ips")

	f.StringVar(&c.Resolv, "resolv", "cp-n", "cp (copy), cp-n (copy if not exist), image (use image), 1.1.1.1;2606:4700:4700::1111 (provide nameservers)")
	f.StringVar(&c.Hosts, "hosts", "cp", "cp (copy), cp-n (copy if not exist), image(use image)")

	f.StringVar(&c.NS.Net, "ns-net", "", "net namespace or host")
	f.StringVar(&c.NS.Pid, "ns-pid", "", "pid namespace or host")
	f.StringVar(&c.NS.Uts, "ns-uts", "", "uts namespace or host")
	f.StringVar(&c.NS.User, "ns-user", "host", "user namespace or host")

	f.StringVar(&c.Devtmpfs, "devtmpfs", "", "mount point of devtmpfs")

	f.Var(&c.Volumes, "v", "volume mount point")

	if err := f.Parse(thisFlags); err != nil {
		return fmt.Errorf("error parsing flags: %v", err)
	}

	if c.RootfsDir == "" {
		c.RootfsDir = defaultRootfs(&c)
	}
	c.Ifaces = []config.NetIface{HostIface}
	PodIface.Main = append(PodIface.Main, HostIface)
	if PodIface.IP == "" {
		PodIface.IP = net.FindFreePodIPs(HostIface.IP)
	}

	if help {
		f.Usage()
		return nil
	}

	if !hasItExecutable(args) {
		return fmt.Errorf("no executable provided")
	}

	if err := container.CheckExistence(&c); err != nil {
		return err
	}

	if c.NS.Net != "host" {

		if HostIface.Type == "bridge" {
			err = net.CreateIface(&c, &HostIface)
			if err != nil {
				return fmt.Errorf("error creating host interface: %v", err)
			}
		}
		err = net.CreateIface(&c, &PodIface)
		if err != nil {
			return fmt.Errorf("error creating pod interface: %v", err)
		}
	}

	// mount squasfs
	err = container.MountRootfs(&c)

	if err != nil {
		return fmt.Errorf("error mount: %v", err)
	}

	if !c.Background {
		defer deRunContainer(&c)
	}

	// Starting proccess
	exitCode, err = container.Start(&c, args)

	if c.Keep {
		c.Status = fmt.Sprintf("exit %d", exitCode)
		if err != nil {
			c.Status = fmt.Sprintf("err %v", err)
		}
		c.SaveConftoDisk()
	}
	return err
}

func defaultRootfs(c *config.Config) string {
	return path.Join(config.Containers, c.Name, "rootfs")
}

func deRunContainer(c *config.Config) {
	if err := container.UmountRootfs(c); err != nil {
		slog.Info("umount", slog.String("err", err.Error()))

	}
	if c.NS.Net != "host" {
		net.Clear(c)
	}

	if !c.Keep {
		if err := os.RemoveAll(c.ContDir()); err != nil {
			slog.Info("removeall", slog.String("err", err.Error()))
		}
	}
}
