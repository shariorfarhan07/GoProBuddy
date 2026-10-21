//go:build !windows && !darwin

package main

import (
	"os/exec"
	"path/filepath"
)

// openFolder shows a folder in the desktop's file manager.
func openFolder(dir string) { _ = exec.Command("xdg-open", dir).Start() }

// revealFile opens the file's folder; xdg-open has no portable "select this file".
func revealFile(path string) { openFolder(filepath.Dir(path)) }
