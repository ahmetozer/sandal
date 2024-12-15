package config

import (
	"math/big"
	"math/rand"
	"path"
	"time"

	"github.com/ahmetozer/sandal/pkg/env"
)

func GenerateContainerId() string {
	time := big.NewInt(time.Now().UnixNano()).Text(62)
	r := big.NewInt(rand.Int63()).Text(62)
	return time + r
}

func (c *Config) ConfigFileLoc() string {
	return ConfigFileLoc(c.Name)
}

func ConfigFileLoc(name string) string {
	return path.Join(env.BaseStateDir, name+".json")
}
