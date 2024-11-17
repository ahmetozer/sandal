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
		slog.Error("Main", slog.String("err", "No argument provided\n\n"))
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
	case "rerun":
		executeSubCommand(rerun)
	case "rm":
		executeSubCommand(rm)
	case "inspect":
		executeSubCommand(inspect)
	case "daemon":
		executeSubCommand(deamon)
	case "cmd":
		executeSubCommand(cmd)
	case "clear":
		executeSubCommand(clear)
	case "exec":
		executeSubCommand(execOnContainer)
	case "help":
		subCommandsHelp()
	default:
		slog.Error("Main", slog.String("err", "Unknown sub command"), slog.String("arg", os.Args[1]))
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
		slog.Error("executeSubCommand", slog.String("command", os.Args[1]), slog.Any("err", err))
		os.Exit(exitCode)
	}
}

func subCommandsHelp() {
	fmt.Printf(`Avaible sub commands:
	run - Run a container
	ps - List containers
	convert - Convert a container image to squashfs
	kill - Kill a container
	rerun - Restart a container
	rm - Remove a container
	inspect - Get configuration file
	cmd - Get execution command
	daemon - Start sandal daemon
	clear - Clear unused containers
	exec - Execute a command in a container
	help - Show this help`)

	fmt.Printf("\n\nVersion: %s\n", BuildVersion)
}
