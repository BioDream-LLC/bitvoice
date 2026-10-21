//go:build !linux

package whisper

import "os/exec"

// tieToBitVoice: elsewhere whisper-server is ended when BitVoice stops it (and a new one takes a new port).
func tieToBitVoice(*exec.Cmd) {}
