package main

import (
	"github.com/ahmetozer/sandal/pkg/cmd"
	"github.com/ahmetozer/sandal/pkg/container/cruntime"
)

func main() {

	if cruntime.IsChild() {
		cruntime.ContainerProc()
	} else {
		cmd.Main()
	}

}
