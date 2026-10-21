package gopro

import (
	"errors"
	"strings"
	"time"

	"goprobuddy/internal/diag"
)

// wifiDevice finds the Wi-Fi interface (usually en0) from networksetup's hardware list.
func wifiDevice() (string, error) {
	out, err := runTool("", "networksetup", "-listallhardwareports")
	if err != nil {
		return "", err
	}
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.Contains(l, "Wi-Fi") || strings.Contains(l, "AirPort") {
			for _, d := range lines[i+1:] {
				if dev, ok := strings.CutPrefix(strings.TrimSpace(d), "Device:"); ok {
					return strings.TrimSpace(dev), nil
				}
			}
		}
	}
	return "", errors.New("no Wi-Fi interface found")
}

// JoinWiFi connects this Mac to the camera's access point using networksetup.
func JoinWiFi(ssid, password string) error {
	diag.Logf("wifi", "joining camera Wi-Fi %q", ssid)
	dev, err := wifiDevice()
	if err != nil {
		return err
	}
	out, err := runTool(password, "networksetup", "-setairportnetwork", dev, ssid, password)
	if err != nil {
		return err
	}
	// networksetup exits 0 even when it fails and prints the reason instead.
	if strings.Contains(strings.ToLower(out), "could not") || strings.Contains(strings.ToLower(out), "error") {
		return errors.New(out)
	}
	return waitForCameraNetwork(ssid, 25*time.Second)
}

// WiFiState describes the Mac's Wi-Fi connection, for logs and error messages.
// Recent macOS versions hide the network name without location permission, so this
// reports whether a camera-network address is present instead.
func WiFiState() string {
	if hasCameraAddress() {
		return "on the camera network"
	}
	return "not on the camera network"
}
