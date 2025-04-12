package daemon

import (
	"log/slog"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

func checkZombie() {

	expiry := time.Now().Add(time.Second * 10)

	for {
		zombieDetected := false
		conts, _ := controller.Containers()
		for _, cont := range conts {
			if cont.Startup && cruntime.IsRunning(cont) {
				slog.Warn("checkZombie", slog.String("cont", cont.Name), slog.Any("pid", cont.ContPid))
				zombieDetected = true
				if time.Now().After(expiry) {
					cruntime.Kill(cont, 9, 0)
				}
			}
		}
		if !zombieDetected {
			return
		}
		time.Sleep(time.Second * 3)
	}

}
