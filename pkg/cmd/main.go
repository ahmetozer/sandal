package cmd

import (
	"fmt"
	"log/slog"
	"os"
)

var (
	BuildVersion = "0.0.0-source"
	BuildTime    = "not presented"
	GitCommit    = "not presented"
)

func Main() {

	if len(os.Args) < 2 {
		slog.Error("No argument provided\n\n")
		subCommandsHelp()
		os.Exit(0)
	}
	switch os.Args[1] {
	case "run":
		executeSubCommand(run)
	case "ps":
		executeSubCommand(ps)
	case "convert":
		executeSubCommand(convert)
	case "kill":
		executeSubCommand(kill)
	case "restart":
		executeSubCommand(restart)
	case "help":
		subCommandsHelp()
	default:
		slog.Error("Unknown sub command", slog.String("arg", os.Args[1]))
		os.Exit(1)
	}
	os.Exit(exitCode)
}

var exitCode = 0

func executeSubCommand(f func([]string) error) {
	err := f(os.Args[2:])
	if err != nil {
		if exitCode == 0 {
			exitCode = 1
		}
		slog.Error(os.Args[1], slog.String("err", err.Error()))
		os.Exit(exitCode)
	}
}

func subCommandsHelp() {
	fmt.Printf(`Avaible sub commands:
	run - Run a container
	ps - List containers
	convert - Convert a container image to squashfs
	kill - Kill a container
	restart - Restart a container
	help - Show this help`)

	fmt.Printf("\n\nVersion: %s\n", BuildVersion)
}
