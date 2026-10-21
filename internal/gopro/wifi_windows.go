package gopro

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"goprobuddy/internal/diag"
)

// JoinWiFi connects this PC to the camera's access point using netsh.
func JoinWiFi(ssid, password string) error {
	esc := func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	profile := fmt.Sprintf(`<?xml version="1.0"?>
<WLANProfile xmlns="http://www.microsoft.com/networking/WLAN/profile/v1">
  <name>%[1]s</name>
  <SSIDConfig><SSID><name>%[1]s</name></SSID></SSIDConfig>
  <connectionType>ESS</connectionType>
  <connectionMode>manual</connectionMode>
  <MSM><security>
    <authEncryption><authentication>WPA2PSK</authentication><encryption>AES</encryption><useOneX>false</useOneX></authEncryption>
    <sharedKey><keyType>passPhrase</keyType><protected>false</protected><keyMaterial>%[2]s</keyMaterial></sharedKey>
  </security></MSM>
</WLANProfile>`, esc(ssid), esc(password))

	diag.Logf("wifi", "joining camera Wi-Fi %q", ssid)
	path := filepath.Join(os.TempDir(), "goprobuddy-wlan.xml")
	if err := os.WriteFile(path, []byte(profile), 0o600); err != nil {
		return err
	}
	defer os.Remove(path)

	run := func(args ...string) error {
		cmd := exec.Command("netsh", args...)
		hideWindow(cmd)
		out, err := cmd.CombinedOutput()
		diag.Logf("wifi", "netsh %s: err=%v output: %s", strings.Join(args[:2], " "), err, bytes.TrimSpace(out))
		if err != nil {
			return fmt.Errorf("netsh %s: %v: %s", args[1], err, bytes.TrimSpace(out))
		}
		return nil
	}
	if err := run("wlan", "add", "profile", "filename="+path, "user=current"); err != nil {
		return err
	}
	if err := run("wlan", "connect", "name="+ssid, "ssid="+ssid); err != nil {
		return err
	}
	// "netsh wlan connect" only queues the request; wait until Windows is really on the
	// camera network, otherwise the first HTTP requests go out over another adapter.
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		state, cur := wifiState()
		if state == "connected" && cur == ssid && hasCameraAddress() {
			diag.Logf("wifi", "on %q with an address on the camera network", ssid)
			return nil
		}
	}
	state, cur := wifiState()
	return fmt.Errorf("Windows did not finish joining %q (Wi-Fi is %s%s)", ssid, state, map[bool]string{true: " to " + cur, false: ""}[cur != ""])
}

// WiFiState describes the PC's Wi-Fi connection, for logs and error messages.
func WiFiState() string {
	state, ssid := wifiState()
	if ssid != "" {
		return state + " to " + ssid
	}
	return state
}

func wifiState() (state, ssid string) {
	cmd := exec.Command("netsh", "wlan", "show", "interfaces")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "unknown", ""
	}
	state = "unknown"
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "State":
			state = strings.TrimSpace(v)
		case "SSID":
			ssid = strings.TrimSpace(v)
		}
	}
	return state, ssid
}
