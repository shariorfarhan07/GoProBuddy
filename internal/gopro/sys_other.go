//go:build !windows

package gopro

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
