package main

import (
	"github.com/ahmetozer/sandal/pkg/cmd"
	"github.com/ahmetozer/sandal/pkg/container"
)

func main() {
	if container.IsChild() {
		container.Exec()
	} else {
		cmd.Main()
	}

}
