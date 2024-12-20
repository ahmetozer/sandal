package env

import (
	"os"
)

func getMain(EnvName, DefaultValue string) string {
	env := os.Getenv(EnvName)
	if env == "" {
		env = DefaultValue
	}
	return env
}

func getCurrents(EnvName, DefaultValue string) string {
	env := getMain(EnvName, DefaultValue)
	for i := range defaults {
		if defaults[i].Name == EnvName {
			defaults[i].Cur = env
		}
	}
	return env
}

func getInit(EnvName, DefaultValue string) string {
	defaults = append(defaults, SandalSystemEnv{Name: EnvName, Def: DefaultValue})
	return DefaultValue
}

type SandalSystemEnv struct {
	Name string
	Def  string
	Cur  string
}
