package cruntime

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"

	"github.com/ahmetozer/sandal/pkg/container/cruntime/net"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Executed at namespace environement before real process
func ContainerInitProc() {
	runtime.GOMAXPROCS(1)

	err := func() error {

		var (
			err error
			c   *config.Config
		)

		// Wait until main proccess resumes

		for retry := 1; retry < 10; retry++ {
			c, err = controller.GetContainer(os.Getenv("SANDAL_CHILD"))
			if err == nil {
				break
			}
			if retry > 5 {
				return fmt.Errorf("unable to load config: %s", err)
			}
			time.Sleep(8 * time.Second)
		}

		if len(c.ContArgs) < 1 {
			return fmt.Errorf("no executable is provided")
		}

		if err := unix.Sethostname([]byte(c.Name)); err != nil {
			return fmt.Errorf("unable to set hostname %s", err)
		}

		if c.NS["net"].Value != "host" {

			k, err := netlink.LinkByName("lo")
			if err == nil {
				netlink.LinkSetUp(k)
			}

			links, err := net.ToLinks(&c.Net)
			if err != nil {
				return fmt.Errorf("unable to set hostname %s", err)
			}

			_, err = links.WaitUntilCreated(5)
			if err != nil {
				return err
			}

			for i := range *links {
				err = (*links)[i].Configure()
				if err != nil {
					return fmt.Errorf("interface error %s, %s", (*links)[i].Id, err)
				}

			}
			IPv4, IPv6 := (*links).FindGateways()
			_, b, _ := net.HasRoute(net.Ipv4DefaultGatewayTestIp())
			if !b {
				net.Ipv4DefaultGatewayTestIp()
				err = netlink.RouteAdd(&netlink.Route{
					Dst: IPv4.IPNet,
					Gw:  IPv4.IP,
				})
				if err != nil {
					json.NewEncoder(os.Stdout).Encode(IPv4)
					return err
				}
			}
			_, b, _ = net.HasRoute(net.Ipv6DefaultGatewayTestIp())
			if !b {
				net.Ipv4DefaultGatewayTestIp()
				err = netlink.RouteAdd(&netlink.Route{
					Dst: IPv6.IPNet,
					Gw:  IPv6.IP,
				})
				if err != nil {
					return err
				}
			}

			err = (*links).RenameLinks()
			if err != nil {
				return err
			}
		}

		err = childSysMounts(c)
		if err != nil {
			return err
		}
		err = childSysNodes(c)
		if err != nil {
			return err
		}
		runCommands(c.RunPrePivot, "/.old_root/")
		purgeOldRoot(c)
		runCommands(c.RunPreExec, "")

		if len(c.ContArgs) == 0 {
			return fmt.Errorf("no container arg providen, malformed container file")
		}
		execPath, err := exec.LookPath(c.ContArgs[0])
		slog.Debug("Exec", slog.String("c.Exec", c.ContArgs[0]), slog.String("execPath", execPath), slog.String("args", strings.Join(c.ContArgs[1:], " ")))
		if err != nil {
			return fmt.Errorf("unable to find %s: %s", c.ContArgs[0], err)
		}

		if c.Dir != "/" {
			os.Chdir(c.Dir)
		}

		// Jump to real process
		if err := unix.Exec(execPath, append([]string{c.ContArgs[0]}, c.ContArgs[1:]...), os.Environ()); err != nil {
			return fmt.Errorf("unable to exec %s: %s", c.ContArgs[0], err)
		}
		return nil
	}()

	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

}
