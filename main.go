package main

import (
	"github.com/ahmetozer/sandal/pkg/cmd"
	"github.com/ahmetozer/sandal/pkg/container"
)

func init() {
	cmd.SetLogLoggerLevel()
}

func main() {

	if container.IsChild() {
		container.Exec()
	} else {
		cmd.Main()
	}

}
