package gopro

import (
	"fmt"
	"os/exec"
	"strings"

	"goprobuddy/internal/diag"
)

// Pair bonds the camera through BlueZ's bluetoothctl. The camera must be in pairing mode the
// first time; afterwards BlueZ reports it as already paired and this returns nil.
func Pair(cam FoundCamera) error {
	addr := cam.Address.String()
	if _, err := exec.LookPath("bluetoothctl"); err != nil {
		diag.Logf("pair", "bluetoothctl not found; relying on BlueZ to pair on first encrypted access")
		return nil
	}
	out, err := exec.Command("bluetoothctl", "--timeout", "20", "pair", addr).CombinedOutput()
	text := strings.TrimSpace(string(out))
	diag.Logf("pair", "bluetoothctl pair %s: err=%v output: %.400s", addr, err, text)
	switch {
	case strings.Contains(text, "Pairing successful"), strings.Contains(text, "AlreadyExists"), strings.Contains(text, "Already paired"):
		_ = exec.Command("bluetoothctl", "trust", addr).Run()
		return nil
	case err != nil || strings.Contains(text, "Failed to pair"):
		return fmt.Errorf("pairing failed — put the camera in pairing mode (Preferences → Wireless Connections → Connect Device) and try again: %s", lastLine(text))
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
