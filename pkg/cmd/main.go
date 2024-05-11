package cmd

import (
	"log/slog"
	"os"
)

func Main() {

	if len(os.Args) < 2 {
		slog.Error("No argument provided")
		os.Exit(0)
	}
	switch os.Args[1] {
	case "run":
		executeSubCommand(run)
	case "ps":
		executeSubCommand(ps)
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
