package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"text/tabwriter"

	"github.com/ahmetozer/sandal/pkg/env"
)

var (
	BuildVersion = "0.0.0-source"
	BuildTime    = "not presented"
	GitCommit    = "not presented"
)

func Main() {

	if len(os.Args) < 2 {
		slog.Error("Main", slog.String("error", "No argument provided"))
		subCommandsHelp()
		os.Exit(0)
	}
	switch os.Args[1] {
	case "run":
		executeSubCommand(Run)
	case "ps":
		executeSubCommand(Ps)
	case "convert":
		executeSubCommand(Convert)
	case "kill":
		executeSubCommand(Kill)
	case "stop":
		executeSubCommand(Stop)
	case "rerun":
		executeSubCommand(Rerun)
	case "rm":
		executeSubCommand(Rm)
	case "inspect":
		executeSubCommand(Inspect)
	case "daemon":
		executeSubCommand(Daemon)
	case "cmd":
		executeSubCommand(Cmd)
	case "clear":
		executeSubCommand(Clear)
	case "exec":
		executeSubCommand(ExecOnContainer)
	case "help":
		subCommandsHelp()
		envs()
	default:
		slog.Error("Main", slog.String("error", "Unknown sub command"), slog.String("arg", os.Args[1]))
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
		slog.Error("executeSubCommand", slog.String("command", os.Args[1]), slog.Any("error", err))
	}
	os.Exit(exitCode)
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
	help - Show help, default and current environment variables` + "\n")

	fmt.Printf("\nVersion: %s\n", BuildVersion)
}

func envs() {
	w := tabwriter.NewWriter(os.Stdout, 7, 1, 0, ' ', 0)

	fmt.Printf("\nSystem variable information:\n")
	fmt.Fprintln(w, " ", "Variable Name", "\t", "Set by user", "\t", "Used as", "\t", "Default")
	for _, r := range env.GetDefaults() {
		fmt.Fprintln(w, " ", r.Name, "\t", env.Get(r.Name, ""), "\t", r.Cur, "\t", r.Def)
	}
	w.Flush()

	// fmt.Printf("Current sandal variables:\n")
	// for _, r := range env.GetCurrents() {
	// 	fmt.Fprintln(w, "\t"+r)
	// }
	// w.Flush()

}
