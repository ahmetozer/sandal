//go:build !linux

package cmd

import "fmt"

var errUnsupported = fmt.Errorf("container commands are only available on Linux")

func Run(args []string) error            { return errUnsupported }
func Ps(args []string) error             { return errUnsupported }
func Kill(args []string) error           { return errUnsupported }
func Stop(args []string) error           { return errUnsupported }
func Rerun(args []string) error          { return errUnsupported }
func Rm(args []string) error             { return errUnsupported }
func Clear(args []string) error          { return errUnsupported }
func ExecOnContainer(args []string) error { return errUnsupported }
func Daemon(args []string) error         { return errUnsupported }
func Inspect(args []string) error        { return errUnsupported }
func Cmd(args []string) error            { return errUnsupported }
