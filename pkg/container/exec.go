package container

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/net"
	"golang.org/x/sys/unix"
)

func Exec() {

	c, err := loadConfig()
	if err != nil {
		log.Fatalf("unable to load config: %s", err)
	}

	if err := unix.Sethostname([]byte(c.Name)); err != nil {
		log.Fatalf("unable to set hostname %s", err)
	}

	configureIfaces(&c)
	childSysMounts(&c)
	childSysNodes(&c)

	_, args := childArgs(os.Args)
	if err := unix.Exec(c.Exec, append([]string{c.Exec}, args...), os.Environ()); err != nil {
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
	var ethNo uint8 = 0
	for i := range c.Ifaces {
		if c.Ifaces[i].ALocFor == config.ALocForPod {

			err = net.WaitInterface(c.Ifaces[i].Name)
			if err != nil {
				log.Fatalf("%s", err)
			}

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

	if err := net.SetInterfaceUp("lo"); err != nil {
		log.Fatalf("unable to set lo up %s", err)
	}
}
