package gopro

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"

	"goprobuddy/internal/diag"
)

// GATT UUIDs from the Open GoPro BLE spec, see docs/PROTOCOL.md §1.
var (
	uuidControlService = bluetooth.New16BitUUID(0xFEA6)
	uuidAPService      = mustUUID("b5f90001-aa8d-11e3-9046-0002a5d5c51b")
	uuidAPSSID         = mustUUID("b5f90002-aa8d-11e3-9046-0002a5d5c51b")
	uuidAPPassword     = mustUUID("b5f90003-aa8d-11e3-9046-0002a5d5c51b")
	uuidCommand        = mustUUID("b5f90072-aa8d-11e3-9046-0002a5d5c51b")
	uuidCommandResp    = mustUUID("b5f90073-aa8d-11e3-9046-0002a5d5c51b")
	uuidSetting        = mustUUID("b5f90074-aa8d-11e3-9046-0002a5d5c51b")
	uuidSettingResp    = mustUUID("b5f90075-aa8d-11e3-9046-0002a5d5c51b")
)

func mustUUID(s string) bluetooth.UUID {
	u, err := bluetooth.ParseUUID(s)
	if err != nil {
		panic(err)
	}
	return u
}

var (
	adapter     = bluetooth.DefaultAdapter
	adapterOnce sync.Once
	adapterErr  error
)

func enableAdapter() error {
	adapterOnce.Do(func() {
		adapterErr = adapter.Enable()
		diag.Logf("ble", "bluetooth adapter enable: err=%v", adapterErr)
	})
	return adapterErr
}

// FoundCamera is a GoPro seen while scanning.
type FoundCamera struct {
	Name    string
	Address bluetooth.Address
	RSSI    int16
}

// Scan looks for GoPro cameras for the given duration and reports each new one.
func Scan(d time.Duration, found func(FoundCamera)) error {
	if err := enableAdapter(); err != nil {
		return fmt.Errorf("bluetooth unavailable: %w", err)
	}
	seen := map[string]bool{}
	diag.Logf("ble", "scan started for %v", d)
	timer := time.AfterFunc(d, func() { _ = adapter.StopScan() })
	defer timer.Stop()
	defer func() { diag.Logf("ble", "scan finished, %d GoPro(s) found", len(seen)) }()
	return adapter.Scan(func(a *bluetooth.Adapter, r bluetooth.ScanResult) {
		name := r.LocalName()
		if !isGoPro(r) {
			return
		}
		key := r.Address.String()
		if seen[key] {
			return
		}
		seen[key] = true
		if name == "" {
			// Cameras often advertise without a name; show the address tail instead.
			name = "GoPro (" + key[len(key)-5:] + ")"
		}
		diag.Logf("ble", "found %q at %s, RSSI %d dBm", name, key, r.RSSI)
		found(FoundCamera{Name: name, Address: r.Address, RSSI: r.RSSI})
	})
}

// goProCompanyID is GoPro's Bluetooth SIG company identifier, sent in manufacturer data.
const goProCompanyID = 0x02F2

// isGoPro matches on manufacturer data, the 0xFEA6 service or the name. The service
// UUID check uses only the first 32 bits because on Windows the advertised 16-bit
// UUID is expanded with a wrong base (seen: 0000fea6-0000-1000-a01e-699601020000).
func isGoPro(r bluetooth.ScanResult) bool {
	for _, m := range r.ManufacturerData() {
		if m.CompanyID == goProCompanyID {
			return true
		}
	}
	for _, u := range r.ServiceUUIDs() {
		if sameShortUUID(u, uuidControlService) {
			return true
		}
	}
	return strings.HasPrefix(r.LocalName(), "GoPro")
}

// sameShortUUID compares the first 32 bits, which are unique for every GoPro UUID we use.
func sameShortUUID(a, b bluetooth.UUID) bool {
	return a.String()[:8] == b.String()[:8]
}

// BLECamera is a connected camera's control link.
type BLECamera struct {
	Address  bluetooth.Address
	dev      bluetooth.Device
	ssid     bluetooth.DeviceCharacteristic
	password bluetooth.DeviceCharacteristic
	command  bluetooth.DeviceCharacteristic
	setting  bluetooth.DeviceCharacteristic
	resp     chan []byte
	mu       sync.Mutex
}

// Connect opens the BLE link and discovers the GoPro characteristics.
// The camera only exposes these on an encrypted link, so on Windows the camera may
// need to be paired once (camera in pairing mode) before this succeeds.
func Connect(cam FoundCamera) (*BLECamera, error) {
	if err := enableAdapter(); err != nil {
		return nil, err
	}
	// The camera hides its services from unpaired devices, so bond first.
	diag.Logf("ble", "connecting to %s (%s)", cam.Name, cam.Address.String())
	if err := Pair(cam); err != nil {
		diag.Logf("ble", "pairing failed: %v", err)
		return nil, err
	}
	dev, err := adapter.Connect(cam.Address, bluetooth.ConnectionParams{ConnectionTimeout: bluetooth.NewDuration(15 * time.Second)})
	if err != nil {
		diag.Logf("ble", "GATT connect failed: %v", err)
		return nil, fmt.Errorf("connect: %w", err)
	}
	diag.Logf("ble", "GATT connected, discovering services")
	c := &BLECamera{Address: cam.Address, dev: dev, resp: make(chan []byte, 8)}
	if err := c.discover(); err != nil {
		diag.Logf("ble", "discovery failed: %v", err)
		// Do not call dev.Disconnect here: in tinygo bluetooth v0.16 on Windows it
		// crashes the process when service discovery failed. Windows drops the
		// idle link on its own.
		return nil, err
	}
	diag.Logf("ble", "control link ready")
	return c, nil
}

func (c *BLECamera) discover() error {
	// Discover everything and match by short UUID rather than filtering, to avoid
	// depending on exact 128-bit UUID conversion on Windows.
	services, err := c.dev.DiscoverServices(nil)
	if err != nil {
		return fmt.Errorf("discover services: %w", err)
	}
	var found int
	diag.Logf("ble", "%d services", len(services))
	for _, s := range services {
		diag.Logf("ble", "service %s", s.UUID().String())
		if !sameShortUUID(s.UUID(), uuidControlService) && !sameShortUUID(s.UUID(), uuidAPService) {
			continue
		}
		chars, err := s.DiscoverCharacteristics(nil)
		if err != nil {
			return fmt.Errorf("discover characteristics: %w", err)
		}
		for _, ch := range chars {
			u := ch.UUID()
			diag.Logf("ble", "  characteristic %s", u.String())
			switch {
			case sameShortUUID(u, uuidAPSSID):
				c.ssid, found = ch, found+1
			case sameShortUUID(u, uuidAPPassword):
				c.password, found = ch, found+1
			case sameShortUUID(u, uuidCommand):
				c.command, found = ch, found+1
			case sameShortUUID(u, uuidSetting):
				c.setting, found = ch, found+1
			case sameShortUUID(u, uuidCommandResp), sameShortUUID(u, uuidSettingResp):
				_ = ch.EnableNotifications(func(buf []byte) {
					b := append([]byte(nil), buf...)
					select {
					case c.resp <- b:
					default:
					}
				})
			}
		}
	}
	diag.Logf("ble", "%d of 4 needed characteristics found", found)
	if found < 4 {
		return errors.New("GoPro characteristics not found — pair the camera first (put it in pairing mode)")
	}
	return nil
}

// write sends one short TLV message. Messages under 20 bytes fit in one packet
// whose header byte is just the payload length (General 5-bit header).
func (c *BLECamera) write(ch bluetooth.DeviceCharacteristic, payload ...byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	pkt := append([]byte{byte(len(payload))}, payload...)
	_, err := ch.Write(pkt)
	return err
}

// EnableWiFi turns on the camera's access point (command 0x17, value 1).
func (c *BLECamera) EnableWiFi() error {
	if err := c.write(c.command, 0x17, 0x01, 0x01); err != nil {
		diag.Logf("ble", "AP enable command write failed: %v", err)
		return fmt.Errorf("enable Wi-Fi: %w", err)
	}
	select {
	case b := <-c.resp:
		diag.Logf("ble", "AP enable response: % x", b)
	case <-time.After(3 * time.Second):
		diag.Logf("ble", "no response to AP enable within 3s, continuing")
	}
	return nil
}

// WiFiCredentials reads the camera's access point SSID and password.
func (c *BLECamera) WiFiCredentials() (ssid, password string, err error) {
	read := func(ch bluetooth.DeviceCharacteristic) (string, error) {
		buf := make([]byte, 64)
		n, err := ch.Read(buf)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(buf[:n]), "\x00"), nil
	}
	if ssid, err = read(c.ssid); err != nil {
		return "", "", fmt.Errorf("read SSID: %w", err)
	}
	if password, err = read(c.password); err != nil {
		return "", "", fmt.Errorf("read password: %w", err)
	}
	diag.Logf("ble", "camera Wi-Fi SSID %q, password %s", ssid, diag.Redact(password))
	return ssid, password, nil
}

// KeepAlive must be sent every few seconds or the camera powers down (setting 0x5B = 0x42).
func (c *BLECamera) KeepAlive() error {
	err := c.write(c.setting, 0x5B, 0x01, 0x42)
	if err != nil {
		diag.Logf("ble", "keep-alive write failed: %v", err)
	}
	return err
}

func (c *BLECamera) Close() error { return c.dev.Disconnect() }
