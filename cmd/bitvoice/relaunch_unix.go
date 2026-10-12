//go:build !windows

package main

import (
	"os"
	"syscall"
)

// relaunch replaces this process with program, keeping the process ID (so a service manager sees the
// same service) and the arguments.
func relaunch(program string, args []string) error {
	env := append(os.Environ(), "BITVOICE_LAUNCHED=1")
	return syscall.Exec(program, append([]string{program}, args...), env)
}
