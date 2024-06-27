package container

import (
	"log"
	"os"
	"path"
	"strings"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/config"
)

func read(file string) *[]byte {
	resolv, err := os.ReadFile(file)
	if err != nil {
		log.Fatalf("unable to read %s: %s", file, err)
	}
	return &resolv

}

func createResolv(c *config.Config, d *[]byte) {
	defer func() {
		d = nil
	}()

	if c.Resolv == "image" {
		return
	}

	if c.Resolv == "cp-n" {
		if file, err := os.Stat("/etc/resolv.conf"); err == nil {
			if file.Size() > 0 {
				return
			}
		}
	}

	if strings.Contains(".", c.Resolv) || strings.Contains(":", c.Resolv) {
		*d = nil
		nameServers := strings.Split(c.Resolv, ";")

		for _, ns := range nameServers {
			*d = append(*d, []byte("nameserver "+ns+"\n")...)
		}
	}

	err := os.MkdirAll(path.Join(sandalChildWorkdir, "/etc"), 0755)
	if err != nil {
		log.Fatalf("unable to create /etc : %s", err)
	}
	if err := os.WriteFile(path.Join(sandalChildWorkdir, "/etc/resolv.conf"), *d, 0644); err != nil {
		log.Fatalf("unable to write /etc/resolv.conf: %s", err)
	}
	mount(path.Join(sandalChildWorkdir, "/etc/resolv.conf"), "/etc/resolv.conf", "tmpfs", syscall.MS_BIND, "ro")
}

func createHosts(c *config.Config, d *[]byte) {
	defer func() {
		d = nil
	}()

	if c.Hosts == "image" {
		return
	}

	if c.Hosts == "cp-n" {
		if file, err := os.Stat("/etc/hosts"); err == nil {
			if file.Size() > 0 {
				return
			}
		}
	}

	err := os.MkdirAll(path.Join(sandalChildWorkdir, "/etc"), 0755)
	if err != nil {
		log.Fatalf("unable to create /etc : %s", err)
	}
	if err := os.WriteFile(path.Join(sandalChildWorkdir, "/etc/hosts"), *d, 0644); err != nil {
		log.Fatalf("unable to write /etc/hosts: %s", err)
	}
	mount(path.Join(sandalChildWorkdir, "/etc/hosts"), "/etc/hosts", "tmpfs", syscall.MS_BIND, "ro")
}
