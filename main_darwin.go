//go:build darwin

package main

import (
	"runtime"

	"github.com/ahmetozer/sandal/pkg/cmd"
)

func init() {
	// Apple Virtualization framework requires all operations on the main thread.
	// Lock the OS thread early to ensure the main goroutine stays on thread 0.
	runtime.LockOSThread()
}

func platformMain() {
	cmd.Main()
}
