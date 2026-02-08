//go:build !windows

package dang

import (
	"os/exec"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// Kill the entire process group
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
