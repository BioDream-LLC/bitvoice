//go:build windows

package agi

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}

func signalHangup(cmd *exec.Cmd) {}

func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}
