package daemon

import (
	_ "embed"
	"errors"
	"log/slog"
	"os"
	"os/exec"
)

//go:embed assets/sandal.init
var initdSandal []byte

//go:embed assets/sandal.service
var systemdSandalService []byte

func InstallServices() error {

	var errs []error
	slog.Info("creating /etc/init.d/sandal")
	err := os.WriteFile("/etc/init.d/sandal", initdSandal, 0o1755)
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
	err = os.WriteFile("/etc/systemd/system/sandal.service", systemdSandalService, 0o1600)
	if err == nil {
		os.Chmod("/etc/systemd/system/sandal.service", 0o1600) // os.write does not set permission for existing file
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
