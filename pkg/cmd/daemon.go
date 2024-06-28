package cmd

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/container"
)

func deamon(args []string) error {

	var (
		install bool
		help    bool
	)
	flags := flag.NewFlagSet("deamon", flag.ExitOnError)

	flags.BoolVar(&install, "install", false, "install service files under /etc/init.d/sandal and /etc/systemd/system/sandal.service")
	flags.BoolVar(&help, "help", false, "show this help message")

	flags.Parse(args)

	if install {
		return installServices()
	}

	updateContainers := make(chan bool, 1)

	conts, _ := config.AllContainers()
	go func() {
		loadConf := func() {
			slog.Debug("reloading configurations")
			newConts, err := config.AllContainers()
			if err == nil {
				conts = newConts
			}
		}
		for {
			select {
			case <-updateContainers:
				loadConf()
			case <-time.After(5 * time.Second):
				loadConf()
			}
		}
	}()

	slog.Info("daemon started")
	if _, err := os.Stat("/etc/init.d/sandal"); err != nil {
		slog.Info(`You can enable sandal deamon at startup with "sandal daemon -install" command.` +
			`It will install service files for systemd and runit`)
	}
	for {
		for _, cont := range conts {
			// oldContPid := cont.ContPid
			if cont.Startup && !container.IsRunning(&cont) {
				container.SendSig(cont.ContPid, 9)
				container.SendSig(cont.HostPid, 9)
				for i := 1; i < 5; i++ {
					newCont, err := config.GetByName(&conts, cont.Name) // in case of new items
					if err != nil {
						slog.Error("cannot get container", slog.String("err", err.Error()))
						break
					}
					// To capture sandal kill event
					if cont.Status != newCont.Status {
						cont = newCont
						break
					}
					// To capture sandal rerun event
					if cont.ContPid != newCont.ContPid {
						cont = newCont
						break
					}
					updateContainers <- true
					time.Sleep(time.Second)
				}

				// If it's killed by operator, do not restart
				if cont.Status == "killed" {
					continue
				}
				kill([]string{cont.Name})
				cont.Background = true
				slog.Info("starting", slog.String("container", cont.Name), slog.Int("oldpid", cont.ContPid))

				if len(cont.HostArgs) < 2 {
					slog.Error("unkown arg size", slog.String("name", cont.Name), slog.String("args", fmt.Sprintf("%v", cont.HostArgs)))
					continue
				}
				cmd := exec.Command("/usr/bin/sandal", cont.HostArgs[1:]...)
				cmd.Stderr = os.Stderr
				cmd.Stdout = os.Stdout
				cmd.Start()
				updateContainers <- true
				for i := 1; i < 5; i++ {
					newCont, err := config.GetByName(&conts, cont.Name) // in case of new items
					if err != nil {
						slog.Error("cannot get container", slog.String("err", err.Error()))
						break
					}
					if cont.ContPid != newCont.ContPid {
						slog.Info("new container started", slog.String("container", cont.Name), slog.Int("pid", newCont.ContPid))
						break
					}
					updateContainers <- true
					slog.Debug("db not updated yet", slog.String("container", cont.Name))
					time.Sleep(time.Second)
				}
				break
			}
		}
		time.Sleep(3 * time.Second)
	}

	// return nil
}

const initdSandal = `#!/bin/sh

### BEGIN INIT INFO
# Provides:		sandal
# Required-Start:	$remote_fs $syslog
# Required-Stop:	$remote_fs $syslog
# Default-Start:	2 3 4 5
# Default-Stop:
# Short-Description:	Sandal daemon
### END INIT INFO

set -e

# /etc/init.d/sandal: start and stop the Sandal daemon


umask 022


. /lib/lsb/init-functions

export PATH="${PATH:+$PATH:}/usr/sbin:/sbin"

SANDAL_OPTS="daemon"

case "$1" in
  start)
	log_daemon_msg "Starting Sandal" "sandal" || true
	# shellcheck disable=SC2086
	if start-stop-daemon --start --quiet --background -m --oknodo --chuid 0:0 --pidfile /run/sandal.pid --exec /bin/sandal -- $SANDAL_OPTS; then
	    log_end_msg 0 || true
	else
	    log_end_msg 1 || true
	fi
	;;
  stop)
	log_daemon_msg "Stopping Sandal" "sandal" || true
	if start-stop-daemon --stop --quiet --oknodo --pidfile /run/sandal.pid --exec /bin/sandal; then
	    log_end_msg 0 || true
	else
	    log_end_msg 1 || true
	fi
	;;

  restart)
	log_daemon_msg "Restarting Sandal" "sandal" || true
	start-stop-daemon --stop --background -m --quiet --oknodo --retry 30 --pidfile /run/sandal.pid --exec /bin/sandal
	# shellcheck disable=SC2086
	if start-stop-daemon --start --quiet --oknodo --chuid 0:0 --pidfile /run/sandal.pid --exec /bin/sandal -- $SANDAL_OPTS; then
	    log_end_msg 0 || true
	else
	    log_end_msg 1 || true
	fi
	;;

  status)
	status_of_proc -p /run/sandal.pid /bin/sandal sandal && exit 0 || exit $?
	;;

  *)
	log_action_msg "Usage: /etc/init.d/sandal {start|stop|restart|status}" || true
	exit 1
esac

exit 0`

const systemdSandalService = `
[Unit]
Description=sandal daemon
After=network.target local-fs.target remote-fs.target

[Service]
User=root
RuntimeDirectory=sandal
LogsDirectory=sandal
StateDirectory=sandal
ExecStart=/usr/bin/sandal daemon
Restart=on-abort

[Install]
WantedBy=multi-user.target`

func installServices() error {

	var errs []error
	slog.Info("creating /etc/init.d/sandal")
	err := os.WriteFile("/etc/init.d/sandal", []byte(initdSandal), 0755)
	if err == nil {
		os.Chmod("/etc/init.d/sandal", 0755) // os.write does not set permission for existing file
		slog.Info("enabling service /etc/init.d/sandal -> /etc/rc2.d/S01sandal")
		err = os.Symlink("/etc/init.d/sandal", "/etc/rc2.d/S01sandal")
		if err != nil {
			errs = append(errs, err)
		}
	} else {
		errs = append(errs, err)
	}

	slog.Info("creating /etc/systemd/system/sandal.service")
	err = os.WriteFile("/etc/systemd/system/sandal.service", []byte(systemdSandalService), 0755)
	if err == nil {
		os.Chmod("/etc/systemd/system/sandal.service", 0755) // os.write does not set permission for existing file
		slog.Info("enabling /etc/systemd/system/sandal.service")
		cmd := exec.Command("systemctl", "enable", "sandal.service")
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		err = cmd.Start()
		if err != nil {
			errs = append(errs, err)
		}
	} else {
		errs = append(errs, err)
	}

	return errors.Join(errs...)

}
