//go:build darwin

package cmd

import "fmt"

var errUnsupported = fmt.Errorf("container commands are only available on Linux (use 'sandal run' with -vm on macOS)")

func Ps(args []string) error              { return errUnsupported }
func Kill(args []string) error            { return errUnsupported }
func Stop(args []string) error            { return errUnsupported }
func Rerun(args []string) error           { return errUnsupported }
func Rm(args []string) error              { return errUnsupported }
func Clear(args []string) error           { return errUnsupported }
func ExecOnContainer(args []string) error { return errUnsupported }
func Daemon(args []string) error          { return errUnsupported }
func Inspect(args []string) error         { return errUnsupported }
func Cmd(args []string) error             { return errUnsupported }
func Snapshot(args []string) error        { return errUnsupported }
func Export(args []string) error          { return errUnsupported }
func Attach(args []string) error          { return errUnsupported }
