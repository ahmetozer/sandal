package container

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/net"
)

func Exec() {

	c, err := loadConfig()
	if err != nil {
		log.Fatalf("unable to load config: %s", err)
	}

	if err := syscall.Sethostname([]byte(c.Name)); err != nil {
		log.Fatalf("unable to set hostname %s", err)
	}

	configureIfaces(&c)
	childMounts(&c)

	if err := net.SetInterfaceUp("lo"); err != nil {
		log.Fatalf("unable to set lo up %s", err)
	}

	_, args := childArgs(os.Args)
	if err := syscall.Exec(c.Exec, append([]string{c.Exec}, args...), os.Environ()); err != nil {
		log.Fatalf("unable to exec %s: %s", c.Exec, err)
	}

}

func loadConfig() (config.Config, error) {

	config := config.Config{}
	confFileLoc := os.Getenv(CHILD_CONFIG_ENV_NAME)
	if confFileLoc == "" {
		return config, fmt.Errorf("config file location not present in env")
	}

	configFile, err := os.ReadFile(confFileLoc)
	if err != nil {
		return config, err
	}

	err = json.Unmarshal(configFile, &config)
	return config, err

}

func configureIfaces(c *config.Config) {
	var err error
	ethNo := 0
	for i := range c.Ifaces {
		if c.Ifaces[i].ALocFor == config.ALocForPod {
			err = net.SetName(c, c.Ifaces[i].Name, fmt.Sprintf("eth%d", ethNo))
			if err != nil {
				log.Fatalf("unable to set name %s", err)
			}

			err = net.AddAddress(c.Ifaces[i].Name, c.Ifaces[i].IP)
			if err != nil {
				log.Fatalf("unable to add address %s", err)
			}

			err = net.SetInterfaceUp(fmt.Sprintf("eth%d", ethNo))
			if err != nil {
				log.Fatalf("unable to set eth%d up %s", ethNo, err)
			}
			if ethNo == 0 {
				net.AddDefaultRoutes(c.Ifaces[i])
			}

			ethNo++
		}
	}
}

func childMounts(c *config.Config) {
	var err error
	mf := uintptr(syscall.MS_PRIVATE | syscall.MS_REC)
	if err := syscall.Mount("", "/", "", mf, ""); err != nil {
		log.Fatalf("unable to mount / as private %s", err)
	}

	mf = uintptr(syscall.MS_BIND | syscall.MS_REC)
	if err := syscall.Mount("rootfs/", "rootfs/", "", mf, ""); err != nil {
		log.Fatalf("unable to bind rootfs/ itself %s", err)
	}

	os.Mkdir("rootfs/.old_root", 0700)
	if err := syscall.PivotRoot("rootfs/", "rootfs/.old_root"); err != nil {
		log.Fatalf("unable to pivot root %s", err)
	}

	if err := os.Chdir("/"); err != nil {
		log.Fatalf("unable to chdir to / %s", err)
	}

	// if err := syscall.Mount("tmpfs", "/tmp", "tmpfs", uintptr(syscall.MS_NODEV), ""); err != nil {
	// 	log.Fatalf("unable to mount /tmp as tmpfs %s", err)
	// }

	if err := syscall.Mount("proc", "/proc", "proc", uintptr(syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC|syscall.MS_RELATIME), ""); err != nil {
		log.Fatalf("unable to mount proc /proc %s", err)
	}

	if c.Devtmpfs != "/dev" {
		if err := syscall.Mount("tmpfs", "/dev", "tmpfs", uintptr(syscall.MS_NOSUID), "size=65536k,mode=755"); err != nil {
			log.Fatalf("unable to mount tmpfs /dev %s", err)
		}
	}
	if c.Devtmpfs != "" {
		os.MkdirAll(c.Devtmpfs, 0755)
		if err := syscall.Mount("tmpfs", c.Devtmpfs, "devtmpfs", uintptr(syscall.MS_NOSUID), "size=65536k,mode=755"); err != nil {
			log.Fatalf("unable to mount devtmpfs %s %s", c.Devtmpfs, err)
		}
	}

	if err := syscall.Mount("sysfs", "/sys", "sysfs", uintptr(syscall.MS_NODEV|syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_RELATIME), "ro"); err != nil {
		log.Fatalf("unable to mount sysfs /sys %s", err)
	}

	os.Mkdir("/dev/pts", 0755)
	if err = syscall.Mount("devpts", "/dev/pts", "devpts", uintptr(syscall.MS_NOSUID|syscall.MS_NOEXEC|syscall.MS_RELATIME), "gid=5,mode=620,ptmxmode=666"); err != nil {
		log.Fatalf("unable to mount devpts /dev/pts %s", err)
	}

	// if err := os.Mkdir("/dev/mqueue", 0755); err != nil {
	// 	log.Fatalf("unable to create dir /dev/mqueue %s", err)
	// }
	// if err = syscall.Mount("mqueue", "/dev/mqueue", "mqueue", uintptr(syscall.MS_NODEV|syscall.MS_NOEXEC|syscall.MS_RELATIME), ""); err != nil {
	// 	log.Fatalf("unable to mount /dev/mqueue %s", err)
	// }

	if err := os.Mkdir("/dev/shm", 0755); err != nil {
		log.Fatalf("unable to create dir /dev/shm %s", err)
	}

	if err = syscall.Mount("shm", "/dev/shm", "tmpfs", uintptr(syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC|syscall.MS_RELATIME), "size=64000k"); err != nil {
		log.Fatalf("unable to mount tmpfs /dev/shm %s", err)
	}

	// mount as private then unmount to remove access to old root
	if err := syscall.Mount("", "/.old_root", "", uintptr(syscall.MS_PRIVATE|syscall.MS_REC), ""); err != nil {
		log.Fatalf("unable to mount /.old_root as private %s", err)
	}

	if err := syscall.Unmount("/.old_root", syscall.MNT_DETACH); err != nil {
		log.Fatalf("unable to unmount /.old_root %s", err)
	}

}
