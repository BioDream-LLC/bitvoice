//go:build windows

package main

import (
	"os"
	"os/exec"
)

// relaunch starts program with the same arguments and ends this process (Windows cannot replace a
// running process).
func relaunch(program string, args []string) error {
	cmd := exec.Command(program, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "BITVOICE_LAUNCHED=1")
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
