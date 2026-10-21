package gopro

import (
	"strings"
	"time"

	"goprobuddy/internal/diag"
)

// JoinWiFi connects this computer to the camera's access point with NetworkManager (nmcli),
// which ships with most desktop Linux distributions.
func JoinWiFi(ssid, password string) error {
	diag.Logf("wifi", "joining camera Wi-Fi %q", ssid)
	if _, err := runTool(password, "nmcli", "device", "wifi", "connect", ssid, "password", password); err != nil {
		return err
	}
	return waitForCameraNetwork(ssid, 25*time.Second)
}

// WiFiState describes the Wi-Fi connection, for logs and error messages.
func WiFiState() string {
	out, err := runTool("", "nmcli", "-t", "-f", "ACTIVE,SSID", "device", "wifi")
	if err != nil {
		return "unknown"
	}
	for _, l := range strings.Split(out, "\n") {
		if ssid, ok := strings.CutPrefix(l, "yes:"); ok {
			return "connected to " + ssid
		}
	}
	return "disconnected"
}
