//go:build windows

package dang

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {
	// No-op on Windows for now
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
