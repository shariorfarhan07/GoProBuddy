package gopro

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"goprobuddy/internal/diag"
)

// pairScript bonds a BLE device through the WinRT custom pairing API (the Go
// bluetooth library cannot pair). The camera must be in pairing mode.
const pairScript = `
param([UInt64]$Address)
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$asTask = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object {
  $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1' })[0]
function Await($op, [Type]$t) { $x = $asTask.MakeGenericMethod($t).Invoke($null, @($op)); $x.Wait(30000) | Out-Null; $x.Result }
[Windows.Devices.Bluetooth.BluetoothLEDevice, Windows.Devices.Bluetooth, ContentType = WindowsRuntime] | Out-Null
[Windows.Devices.Enumeration.DevicePairingKinds, Windows.Devices.Enumeration, ContentType = WindowsRuntime] | Out-Null
$dev = Await ([Windows.Devices.Bluetooth.BluetoothLEDevice]::FromBluetoothAddressAsync($Address)) ([Windows.Devices.Bluetooth.BluetoothLEDevice])
if (-not $dev) { 'RESULT:NotFound'; exit }
$p = $dev.DeviceInformation.Pairing
if ($p.IsPaired) { 'RESULT:AlreadyPaired'; exit }
$c = $p.Custom
$h = [Windows.Foundation.TypedEventHandler[Windows.Devices.Enumeration.DeviceInformationCustomPairing, Windows.Devices.Enumeration.DevicePairingRequestedEventArgs]] { param($s, $e) $e.Accept() }
$t = $c.add_PairingRequested($h)
$k = [Windows.Devices.Enumeration.DevicePairingKinds]::ConfirmOnly -bor [Windows.Devices.Enumeration.DevicePairingKinds]::ConfirmPinMatch
$r = Await ($c.PairAsync($k, [Windows.Devices.Enumeration.DevicePairingProtectionLevel]::Encryption)) ([Windows.Devices.Enumeration.DevicePairingResult])
$c.remove_PairingRequested($t)
"RESULT:$($r.Status)"
`

// Pair bonds the camera with Windows. It returns nil if the camera is (now) paired.
func Pair(cam FoundCamera) error {
	var addr uint64
	for _, b := range cam.Address.MAC.Address() { // big-endian, as WinRT expects
		addr = addr<<8 | uint64(b)
	}
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command",
		"& {"+pairScript+"} -Address "+fmt.Sprint(addr))
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	diag.Logf("pair", "pairing helper for %s: err=%v output: %s", cam.Address.String(), err, bytes.TrimSpace(out))
	if err != nil {
		return fmt.Errorf("pairing helper failed: %v: %s", err, bytes.TrimSpace(out))
	}
	s := string(out)
	i := strings.LastIndex(s, "RESULT:")
	if i < 0 {
		return fmt.Errorf("pairing: unexpected output: %s", strings.TrimSpace(s))
	}
	switch res := strings.TrimSpace(s[i+len("RESULT:"):]); res {
	case "Paired", "AlreadyPaired":
		return nil
	default:
		return fmt.Errorf("pairing %s — put the camera in pairing mode (Preferences → Wireless Connections → Connect Device) and try again", res)
	}
}
