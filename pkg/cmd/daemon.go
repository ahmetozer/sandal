package cmd

import (
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/container"
)

func deamon(args []string) error {

	config.SetModeDeamon()

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

	go config.Server()

	conts, _ := config.Containers()
	go func() {
		loadConf := func() {
			slog.Debug("deamon", slog.String("aciton", "reloading configurations"))
			newConts, err := config.Containers()
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

	slog.Info("daemon", slog.String("message", "sandal daemon service started"))
	if _, err := os.Stat("/etc/init.d/sandal"); err != nil {
		slog.Info("deamon", slog.String("message", `You can enable sandal deamon at startup with "sandal daemon -install" command.`+
			`It will install service files for systemd and runit`))
	}

	killRequested := false
	go func() {
		done := make(chan os.Signal, 1)
		for {
			signal.Notify(done, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
			sig := <-done
			for _, cont := range conts {
				// oldContPid := cont.ContPid
				if cont.Startup && container.IsRunning(&cont) {
					syscall.Kill(cont.ContPid, sig.(syscall.Signal))
				}
			}
			syscall.Kill(os.Getpid(), sig.(syscall.Signal))
			if sig == syscall.SIGTERM || sig == syscall.SIGINT || sig == syscall.SIGKILL || sig == syscall.SIGQUIT {
				killRequested = true
				break
			}
		}
	}()

ContHealthCheck:
	for {
		for _, cont := range conts {

			// oldContPid := cont.ContPid
			if cont.Startup && !container.IsRunning(&cont) {
				if killRequested {
					break ContHealthCheck
				}
				container.SendSig(cont.ContPid, 9)
				container.SendSig(cont.HostPid, 9)
				for i := 1; i < 5; i++ {
					newCont, err := config.GetByName(&conts, cont.Name) // in case of new items
					if err != nil {
						slog.Error("deamon", slog.String("GetByName", "cannot get container"), slog.Any("error", err))
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
				slog.Info("deamon", slog.String("action", "starting"), slog.String("container", cont.Name), slog.Int("oldpid", cont.ContPid))

				if len(cont.HostArgs) < 2 {
					slog.Error("deamon", slog.String("error", "unkown arg size"), slog.String("name", cont.Name), slog.String("args", strings.Join(cont.HostArgs, " ")))
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
						slog.Warn("deamon", slog.String("GetByName", "cannot get container"), slog.String("name", cont.Name), slog.Any("error", err))
						break
					}
					if cont.ContPid != newCont.ContPid {
						slog.Info("deamon", slog.String("message", "new container started"), slog.String("name", cont.Name), slog.Int("pid", newCont.ContPid))
						break
					}
					updateContainers <- true
					slog.Debug("deamon", slog.String("message", "db not updated yet"), slog.String("name", cont.Name))
					time.Sleep(time.Second)
				}
				break
			}
		}
		time.Sleep(3 * time.Second)
	}

	return nil
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
	start-stop-daemon --stop --quiet --oknodo --retry 30 --pidfile /run/sandal.pid --exec /bin/sandal
	# shellcheck disable=SC2086
	if start-stop-daemon --start --background -m --quiet --oknodo --chuid 0:0 --pidfile /run/sandal.pid --exec /bin/sandal -- $SANDAL_OPTS; then
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
	err := os.WriteFile("/etc/init.d/sandal", []byte(initdSandal), 0o1755)
	if err == nil {
		os.Chmod("/etc/init.d/sandal", 0o1755) // os.write does not set permission for existing file
		slog.Info("installServices", slog.String("enabling service", "/etc/init.d/sandal -> /etc/rc2.d/S01sandal"))
		err = os.Symlink("/etc/init.d/sandal", "/etc/rc2.d/S01sandal")
		if err != nil {
			errs = append(errs, err)
		}
	} else {
		errs = append(errs, err)
	}

	slog.Info("installServices", slog.String("enabling service", "creating /etc/systemd/system/sandal.service"))
	err = os.WriteFile("/etc/systemd/system/sandal.service", []byte(systemdSandalService), 0o1755)
	if err == nil {
		os.Chmod("/etc/systemd/system/sandal.service", 0o1755) // os.write does not set permission for existing file
		slog.Info("installServices", slog.String("enabling service", "/etc/systemd/system/sandal.service -> /etc/rc2.d/S01sandal"))
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
