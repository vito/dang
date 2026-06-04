//go:build js

package dang

import "os/exec"

// The js/wasm build (used by the docs playground) has no process spawning,
// so these are no-ops.

func setProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) error { return nil }
