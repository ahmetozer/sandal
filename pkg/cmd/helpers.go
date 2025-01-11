package cmd

import "fmt"

// returns flags and child proccess args
func SplitFlagsArgs(args []string) (flagArgs []string, commandArgs []string, err error) {

	for childArgStartLoc, arg := range args {
		if arg == "--" {
			hostArgs := args[:childArgStartLoc]
			podArgs := args[childArgStartLoc+1:]
			if len(podArgs) < 1 {
				return hostArgs, podArgs, fmt.Errorf("there is no command provided")
			}
			return hostArgs, podArgs, nil
		}
	}
	return args, nil, fmt.Errorf("there is no command provided")

}
