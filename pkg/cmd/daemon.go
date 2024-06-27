package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/container"
)

func deamon(args []string) error {

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
				cmd := exec.Command(os.Args[0], cont.HostArgs[1:]...)
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
