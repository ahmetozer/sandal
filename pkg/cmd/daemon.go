package cmd

import (
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/daemon"
)

func Daemon(args []string) error {

	var (
		install bool
		help    bool
	)
	dc := daemon.DaemonConfig{}

	flags := flag.NewFlagSet("daemon", flag.ExitOnError)
	flags.BoolVar(&install, "install", false, "install service files under /etc/init.d/sandal and /etc/systemd/system/sandal.service")
	flags.DurationVar(&dc.DiskReloadInterval, "read-interval", 0*time.Second, "user read interval instead of file events")
	flags.BoolVar(&help, "help", false, "show this help message")

	flags.Parse(args)

	if install {
		return daemon.InstallServices()
	}

	if _, err := os.Stat("/etc/init.d/sandal"); err != nil {
		slog.Info("daemon", slog.String("message", `You can enable sandal daemon at startup with 'sandal daemon -install' command.`+
			`It will install service files for systemd and runit`))
	}

	go controller.Server()

	return dc.Start()
}
