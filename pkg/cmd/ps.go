package cmd

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/container"
)

func ps(args []string) error {
	conts := config.AllContainers()
	flags := flag.NewFlagSet("ps", flag.ExitOnError)
	var (
		help   bool
		ns     bool
		verify bool
	)

	flags.BoolVar(&help, "help", false, "show this help message")
	flags.BoolVar(&verify, "verify", false, "verify running state containers (sig 0)")
	flags.BoolVar(&ns, "ns", false, "show namespaces")

	flags.Parse(args)

	if help {
		flags.PrintDefaults()
		return nil
	}

	header := "NAME\tSQUASHFS\tCOMMAND\tCREATED\tSTATUS\tPID"
	printFunc := printDefault
	if verify {
		printFunc = printVerified
	}

	if ns {
		header = "NAME\tPID\tCGROUPNS\tIPC\tMNT\tNET\tPIDNS\tUSERNS\tUTS"
		printFunc = printNamespaces
	}

	t := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
	fmt.Fprintln(t, header)
	defer t.Flush()
	for _, c := range conts {
		printFunc(&c, t)
	}
	return nil
}

func printVerified(c *config.Config, t *tabwriter.Writer) {
	if c.Status == container.ContainerStatusRunning {
		if !container.IsRunning(c) {
			c.Status = container.ContainerStatusHang
		}
	}
	printDefault(c, t)
}

func printDefault(c *config.Config, t *tabwriter.Writer) {
	created := time.Unix(c.Created, 0).Format(time.RFC3339)
	fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%d\n", c.Name, c.SquashfsFile, c.Exec, created, c.Status, c.ContPid)
}

func printNamespaces(c *config.Config, t *tabwriter.Writer) {
	fmt.Fprintf(t, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", c.Name, c.ContPid, c.NS.Cgroup, c.NS.Ipc, c.NS.Mnt, c.NS.Net, c.NS.Pid, c.NS.User, c.NS.Uts)
}
