package main

import "os/exec"

// openFolder shows a folder in Explorer.
func openFolder(dir string) { _ = exec.Command("explorer", dir).Start() }

// revealFile opens Explorer with the file selected.
func revealFile(path string) { _ = exec.Command("explorer", "/select,", path).Start() }
