package gopro

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"goprobuddy/internal/diag"
)

// hasCameraAddress reports whether DHCP has given this computer an address on the camera's
// network, 10.5.5.0/24. That is the reliable sign the Wi-Fi join finished on every OS.
func hasCameraAddress() bool {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			if ip := n.IP.To4(); ip != nil && ip[0] == 10 && ip[1] == 5 && ip[2] == 5 {
				return true
			}
		}
	}
	return false
}

// waitForCameraNetwork polls until this computer has a camera-network address.
func waitForCameraNetwork(ssid string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if hasCameraAddress() {
			diag.Logf("wifi", "on %q with an address on the camera network", ssid)
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("did not get an address on %q's network within %v (Wi-Fi: %s)", ssid, timeout, WiFiState())
}

// runTool runs a system command, logs it without secrets, and returns its trimmed output.
func runTool(redact string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	shown := strings.Join(append([]string{name}, args...), " ")
	if redact != "" {
		shown = strings.ReplaceAll(shown, redact, diag.Redact(redact))
	}
	text := strings.TrimSpace(string(out))
	diag.Logf("wifi", "%s: err=%v output: %.300s", shown, err, text)
	if err != nil {
		return text, fmt.Errorf("%s: %v: %s", name, err, text)
	}
	return text, nil
}
