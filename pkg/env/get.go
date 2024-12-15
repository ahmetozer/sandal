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
		if defaults[i].name == EnvName {
			defaults[i].cur = env
		}
	}
	return env
}

func getInit(EnvName, DefaultValue string) string {
	defaults = append(defaults, sEnv{name: EnvName, def: DefaultValue})
	return DefaultValue
}

type sEnv struct {
	name string
	def  string
	cur  string
}
