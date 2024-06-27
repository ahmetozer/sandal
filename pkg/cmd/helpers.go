package cmd

// returns flags and child proccess args
func SplitFlagsArgs(args []string) ([]string, []string) {

	if !hasItExecutable(args) {
		return args, []string{}
	}
	for childArgStartLoc, arg := range args {
		if arg[0] != '-' {
			return args[:childArgStartLoc], args[childArgStartLoc:]
		}
	}
	return []string{}, args

}

func hasItExecutable(args []string) bool {
	for _, arg := range args {
		if arg[0] != '-' {
			return true
		}
	}
	return false
}
