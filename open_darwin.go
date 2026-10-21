package main

import "os/exec"

// openFolder shows a folder in Finder.
func openFolder(dir string) { _ = exec.Command("open", dir).Start() }

// revealFile opens Finder with the file selected.
func revealFile(path string) { _ = exec.Command("open", "-R", path).Start() }
