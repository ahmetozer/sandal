package config

import (
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"path"
	"time"
)

// ContDir returns the work directory for curret container
func (c *Config) ContDir() string {
	return path.Join(Containers, c.Name)
}

func (c *Config) ConfigFileLoc() string {
	return path.Join(c.ContDir(), "config.json")
}

func (c *Config) SaveConftoDisk() error {
	if err := os.WriteFile(c.ConfigFileLoc(), c.Json(), 0644); err != nil {
		return fmt.Errorf("writing config file: %v", err)
	}
	return nil
}

func GenerateContainerId() string {
	time := big.NewInt(time.Now().UnixNano()).Text(62)
	r := big.NewInt(rand.Int63()).Text(62)
	return time + r
}
