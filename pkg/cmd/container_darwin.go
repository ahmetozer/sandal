//go:build darwin

package cmd

import "fmt"

var errPhase2 = fmt.Errorf("this command requires vsock communication with the VM (planned for Phase 2)")

func ExecOnContainer(args []string) error { return errPhase2 }
func Daemon(args []string) error          { return errPhase2 }
func Snapshot(args []string) error        { return errPhase2 }
func Export(args []string) error          { return errPhase2 }
func Attach(args []string) error          { return errPhase2 }
