package config

import (
	"fmt"
	"log/slog"
	"math/big"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"time"
)

func (c *Config) ConfigFileLoc() string {
	return path.Join(BaseStateDir, c.Name+".json")
}

func (c *Config) SaveConftoDisk() error {
	savePath := filepath.Dir(c.ConfigFileLoc())
	slog.Debug("ConfigFileLoc", slog.String("action", "saving config file"), slog.String("file", c.ConfigFileLoc()))
	retry := false
WriteFile:
	err := os.WriteFile(c.ConfigFileLoc(), c.Json(), 0644)
	if err != nil {
		if os.IsNotExist(err) && !retry {
			err := os.MkdirAll(savePath, 0755)
			slog.Debug("ConfigFileLoc", slog.String("action", "mkdir conf path"), slog.String("path", savePath), slog.Any("err", err))
			retry = true
			goto WriteFile
		}
		return fmt.Errorf("writing config file: %v", err)
	}
	return nil
}

func GetByName(conts *[]Config, name string) (Config, error) {
	for _, c := range *conts {
		if c.Name == name {
			return c, nil
		}
	}
	return Config{}, fmt.Errorf("container not found")
}

func GenerateContainerId() string {
	time := big.NewInt(time.Now().UnixNano()).Text(62)
	r := big.NewInt(rand.Int63()).Text(62)
	return time + r
}
