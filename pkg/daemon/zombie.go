package daemon

import (
	"log/slog"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

func checkZombie() {

	for {
		zombieDetected := false
		conts, _ := controller.Containers()
		for _, cont := range conts {
			// oldContPid := cont.ContPid
			if cont.Startup && cruntime.IsRunning(cont) {
				slog.Warn("checkZombie", slog.String("cont", cont.Name), slog.Any("pid", cont.ContPid))
				zombieDetected = true
			}
		}
		if !zombieDetected {
			return
		}
		time.Sleep(time.Second * 3)
	}

}
