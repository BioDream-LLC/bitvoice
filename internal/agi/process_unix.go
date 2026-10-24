//go:build !windows

package agi

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the program in a process group of its own, so killing it also ends what it started.
func setProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func signalHangup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Signal(syscall.SIGHUP)
	}
}

func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Process.Kill()
	}
}
