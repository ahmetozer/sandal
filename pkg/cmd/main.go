package cmd

import (
	"log/slog"
	"os"
)

func Main() {

	if len(os.Args) < 2 {
		slog.Error("No argument provided")
		mainHelp()
		os.Exit(0)
	}
	switch os.Args[1] {
	case "run":
		err := run(os.Args[2:])
		if err != nil {
			slog.Error("run", slog.String("err", err.Error()))
			os.Exit(1)
		}
	default:
		slog.Error("Unknown sub command", slog.String("arg", os.Args[1]))
		os.Exit(1)
	}
}

func mainHelp() {

}
