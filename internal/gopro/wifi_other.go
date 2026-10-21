//go:build !windows && !darwin && !linux

package gopro

import "errors"

// JoinWiFi is automated on Windows, macOS and Linux; elsewhere join the camera network manually.
func JoinWiFi(ssid, password string) error {
	return errors.New("automatic Wi-Fi join is not supported on this system; join " + ssid + " manually")
}

// WiFiState is not implemented on this system.
func WiFiState() string { return "unknown" }
