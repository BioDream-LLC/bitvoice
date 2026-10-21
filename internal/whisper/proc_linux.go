package whisper

import (
	"os/exec"
	"syscall"
)

// tieToBitVoice has Linux end whisper-server if BitVoice itself is killed, so none is left running.
func tieToBitVoice(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
