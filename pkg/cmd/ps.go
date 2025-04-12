package cmd

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

func Ps(args []string) error {
	conts, _ := controller.Containers()
	flags := flag.NewFlagSet("ps", flag.ExitOnError)
	var (
		help bool
		ns   bool
		dry  bool
	)

	flags.BoolVar(&help, "help", false, "show this help message")
	flags.BoolVar(&dry, "dry", false, "do not verify running state containers")
	flags.BoolVar(&ns, "ns", false, "show namespaces")

	flags.Parse(args)

	if help {
		flags.PrintDefaults()
		return nil
	}

	header := "NAME\tLOWER\tCOMMAND\tCREATED\tSTATUS\tPID"
	printFunc := printVerified
	if dry {
		printFunc = printDry
	}

	if ns {
		header = "NAME\tPID\tCGROUPNS\tIPC\tMNT\tNET\tPIDNS\tUSERNS\tUTS"
		printFunc = printNamespaces
	}

	t := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
	fmt.Fprintln(t, header)
	defer t.Flush()
	for _, c := range conts {
		printFunc(c, t)
	}
	return nil
}

func printVerified(c *config.Config, t *tabwriter.Writer) {
	if c.Status == cruntime.ContainerStatusRunning {
		if !cruntime.IsRunning(c) {
			c.Status = cruntime.ContainerStatusHang
		}
	}
	printDry(c, t)
}

func printDry(c *config.Config, t *tabwriter.Writer) {
	created := time.Unix(c.Created, 0).Format(time.RFC3339)
	executable := "undefined"
	if len(c.ContArgs) >= 1 {
		executable = c.ContArgs[0]
	}
	fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%d\n", c.Name, c.Lower.String(), executable, created, c.Status, c.ContPid)
}

func printNamespaces(c *config.Config, t *tabwriter.Writer) {

	fmt.Fprintf(t, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", c.Name, c.ContPid, c.NS["cgroup"].Value, c.NS["ipc"].Value, c.NS["mnt"].Value, c.NS["net"].Value, c.NS["pid"].Value, c.NS["user"].Value, c.NS["uts"].Value)
}
