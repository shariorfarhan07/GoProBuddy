//go:build !windows && !linux

package gopro

// Pair is a no-op here: macOS (CoreBluetooth) pairs on the first encrypted access.
func Pair(cam FoundCamera) error { return nil }
