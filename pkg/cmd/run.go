package cmd

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/container"
	"github.com/ahmetozer/sandal/pkg/net"
)

func run(args []string) error {

	if len(args) < 1 {
		return fmt.Errorf("no command option provided")
	}

	c := config.NewContainer()
	var (
		help      bool
		thisFlags []string
		err       error
	)
	thisFlags, c.PodArgs = SplitFlagsArgs(args)
	slog.Debug("run", slog.Any("thisFlags", thisFlags), slog.Any("podArgs", c.PodArgs), slog.Any("args", os.Args))
	c.HostArgs = os.Args
	f := flag.NewFlagSet("run", flag.ExitOnError)

	containerId := config.GenerateContainerId()

	f.BoolVar(&help, "help", false, "show this help message")
	f.BoolVar(&c.Background, "d", false, "run container in background")
	f.StringVar(&c.Name, "name", containerId, "name of the container")
	f.BoolVar(&c.ReadOnly, "ro", false, "read only rootfs")

	f.BoolVar(&c.Remove, "rm", false, "remove container files on exit")

	f.BoolVar(&c.Startup, "startup", false, "run container at startup by sandal daemon")

	f.BoolVar(&c.EnvAll, "env-all", false, "send all enviroment variables to container")
	f.Var(&c.PassEnv, "env-pass", "pass only requested enviroment variables to container")
	f.StringVar(&c.Dir, "dir", "", "working directory")

	f.UintVar(&c.TmpSize, "tmp", 0, "allocate changes at memory instead of disk. unit is in MB, disk is used used by default")

	var HostIface = config.NetIface{ALocFor: config.ALocForHost}
	f.StringVar(&HostIface.Type, "net-type", "bridge", "bridge, macvlan, ipvlan")
	f.StringVar(&HostIface.Name, "host-net", "sandal0", "host interface for bridge or macvlan")
	f.StringVar(&HostIface.IP, "host-ips", config.DefaultHostNet, "host interface ips")

	var PodIface = config.NetIface{Type: "veth"}
	f.StringVar(&PodIface.IP, "pod-ips", "", "container interface ips")

	f.StringVar(&c.Resolv, "resolv", "cp", "cp (copy), cp-n (copy if not exist), image (use image), 1.1.1.1;2606:4700:4700::1111 (provide nameservers)")
	f.StringVar(&c.Hosts, "hosts", "cp", "cp (copy), cp-n (copy if not exist), image(use image)")

	for _, k := range config.Namespaces {
		defaultValue := ""
		if k == "user" {
			defaultValue = "host"
		}
		f.StringVar(&c.NS[k].Value, "ns-"+k, defaultValue, fmt.Sprintf("%s namespace or host", k))
	}

	f.StringVar(&c.Workdir, "wdir", config.Defs(containerId).Workdir, "overlay fs workdir")
	f.StringVar(&c.Upperdir, "udir", config.Defs(containerId).UpperDir, "container changes will saved this directory")
	f.StringVar(&c.RootfsDir, "rdir", config.Defs(containerId).RootFsDir, "root directory of operating system")

	f.Var(&c.Volumes, "v", "volume mount point")
	f.Var(&c.Lower, "lw", "you can merge multiple lowerdirs")

	f.StringVar(&c.Devtmpfs, "devtmpfs", "", "mount point of devtmpfs")

	f.Var(&c.RunPrePivot, "rpp", "run command before pivoting to container rootfs")
	f.Var(&c.RunPreExec, "rpe", "run command before executing init")

	if err := f.Parse(thisFlags); err != nil {
		return fmt.Errorf("error parsing flags: %v", err)
	}

	// Flag does not have order while parsing
	// If the name is presented and values are not filled by arguments
	// re-fill values with new defaults.
	if containerId != c.Name {
		if c.Workdir == config.Defs(containerId).Workdir {
			c.Workdir = config.Defs(c.Name).Workdir
		}
		if c.Upperdir == config.Defs(containerId).UpperDir {
			c.Upperdir = config.Defs(c.Name).UpperDir
		}
		if c.RootfsDir == config.Defs(containerId).RootFsDir {
			c.RootfsDir = config.Defs(c.Name).RootFsDir
		}
	}

	if help {
		f.Usage()
		return nil
	}

	if !hasItExecutable(args) {
		slog.Debug("no executable provided", slog.String("args", strings.Join(args, " ")))
		return fmt.Errorf("no executable provided")
	}

	if err := container.CheckExistence(&c); err != nil {
		return err
	}

	if c.Status == container.ContainerStatusRunning {
		return fmt.Errorf("container %s is already running", c.Name)
	}

	deRunContainer(&c)

	if c.Startup && !c.Background {
		return fmt.Errorf("startup only works with background mode, please enable with '-d' arg")
	}

	c.Ifaces = []config.NetIface{HostIface}
	PodIface.Main = append(PodIface.Main, HostIface)
	if PodIface.IP == "" {
		PodIface.IP, err = net.FindFreePodIPs(HostIface.IP)
		// slog.Debug("no executable provided", slog.String("args", strings.Join(args, " ")))
		slog.Debug("network interfaces", slog.String("podIface", fmt.Sprintf("%+v", PodIface)), slog.String("hostIface", fmt.Sprintf("%+v", HostIface)))
		if err != nil {
			return err
		}
	}
	err = Start(&c, HostIface, PodIface)
	deRunContainer(&c)
	return err
}

func Start(c *config.Config, HostIface, PodIface config.NetIface) error {
	// Starting container
	var err error
	if c.NS["net"].Value != "host" {

		if HostIface.Type == "bridge" {
			err = net.CreateIface(c, &HostIface)
			if err != nil {
				return fmt.Errorf("error creating host interface: %v", err)
			}
		}
		err = net.CreateIface(c, &PodIface)
		if err != nil {
			return fmt.Errorf("error creating pod interface: %v", err)
		}
	}

	// mount squasfs
	err = container.MountRootfs(c)
	if err != nil {
		return fmt.Errorf("error mount: %v", err)
	}

	// Starting proccess
	exitCode, err = container.Start(c, c.PodArgs)

	if !c.Remove {
		c.Status = fmt.Sprintf("exit %d", exitCode)
		if err != nil {
			c.Status = fmt.Sprintf("err %v", err)
		}
		c.SaveConftoDisk()
	}
	return err
}

func deRunContainer(c *config.Config) {
	if err := container.UmountRootfs(c); err != nil {
		for _, e := range err {
			slog.Debug("deRunContainer", "umount", slog.Any("err", e))
		}
	}
	if c.NS["net"].Value != "host" {
		net.Clear(c)
	}

	if c.Remove {

		removeAll := func(name string) {
			if err := os.RemoveAll(name); err != nil {
				slog.Debug("deRunContainer", "removeall", slog.String("file", name), slog.Any("err", err))
			}
		}

		removeAll(c.RootfsDir)
		removeAll(c.Workdir)
		removeAll(c.Upperdir)
		removeAll(c.ConfigFileLoc())
	}

}
