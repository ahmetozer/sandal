package config

import "os"

func GetEnv(EnvName, DefaultValue string) string {
	env := os.Getenv(EnvName)
	if env == "" {
		return DefaultValue
	}
	return env
}
