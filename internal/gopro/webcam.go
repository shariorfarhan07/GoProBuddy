package gopro

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"net"
	"time"

	"goprobuddy/internal/diag"
)

// Webcam choices from the Open GoPro webcam API (docs/PROTOCOL.md §6).
var (
	WebcamResolutions = []SettingOption{{12, "1080p"}, {7, "720p"}, {4, "480p"}}
	WebcamFOVs        = []SettingOption{{0, "Wide"}, {4, "Linear"}, {2, "Narrow"}, {3, "SuperView"}}
)

// WebcamPort is the local UDP port the camera streams webcam video to. HERO9 firmware
// ignores the port parameter and always sends to 8554, so ask for that everywhere.
const WebcamPort = 8554

// IsUSBAddress reports whether ip is a camera's USB (NCM) address, 172.2X.1YZ.51.
func IsUSBAddress(ip string) bool {
	p := net.ParseIP(ip).To4()
	return p != nil && p[0] == 172 && p[1] >= 20 && p[1] <= 29 && p[2] >= 100 && p[2] <= 199
}

// USBCameraAddress is FindUSBCamera without logging, for polling.
func USBCameraAddress() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && IsUSBAddress(n.IP.String()) {
			p := n.IP.To4()
			return fmt.Sprintf("172.%d.%d.51", p[1], p[2])
		}
	}
	return ""
}

// WebcamError is the camera's reason for refusing a webcam request
// ({"status":N,"error":N} in the Open GoPro webcam API).
type WebcamError struct {
	Code int
	USB  bool // the request went over USB-C
}

func (e *WebcamError) Error() string {
	switch e.Code {
	case 1:
		return "the camera could not switch to its webcam preset"
	case 2:
		return "the camera could not set the webcam resolution"
	case 3:
		return "the camera could not start the webcam stream"
	case 4:
		return "the camera is busy (is it recording?)"
	case 5:
		return "the camera timed out starting webcam mode"
	case 6:
		return "the camera does not support that resolution or lens; try 1080p or 720p with Wide"
	case 7:
		if !e.USB {
			return "the camera says webcam mode is unavailable right now. Make sure it isn't recording or in a menu and try again; if it keeps refusing, connect it with a USB-C cable (USB Connection: GoPro Connect), which is the most reliable way on HERO9–HERO11"
		}
		return "the camera says webcam mode is unavailable right now; make sure it isn't recording or in a menu, and that its USB Connection is set to GoPro Connect"
	case 8:
		return "the camera is still leaving webcam mode; wait a moment and try again"
	}
	return fmt.Sprintf("the camera refused webcam mode (error %d)", e.Code)
}

// webcamErr extracts the camera's error code from a failed webcam request's body.
func webcamErr(body []byte, usb bool) error {
	var r struct {
		Error *int `json:"error"`
	}
	if json.Unmarshal(body, &r) == nil && r.Error != nil && *r.Error != 0 {
		return &WebcamError{Code: *r.Error, USB: usb}
	}
	return nil
}

// FindUSBCamera returns the address of a GoPro plugged in over USB-C, or "" if there is none.
// The camera hands the PC an address on 172.2X.1YZ.0/24 and serves itself on .51.
func FindUSBCamera() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		diag.Logf("usb", "cannot list network interfaces: %v", err)
		return ""
	}
	var seen []string
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := n.IP.String()
		seen = append(seen, ip)
		if IsUSBAddress(ip) {
			p := n.IP.To4()
			cam := fmt.Sprintf("172.%d.%d.51", p[1], p[2])
			diag.Logf("usb", "PC has USB camera link address %s, camera should be %s", ip, cam)
			return cam
		}
	}
	diag.Logf("usb", "no GoPro USB link; PC addresses: %v", seen)
	return ""
}

// RunWebcam puts the camera in webcam mode and decodes the stream into onFrame until ctx is
// cancelled. With onFrame nil the stream is left on udp://0.0.0.0:<port> for another app
// (OBS, VLC) to open, and RunWebcam just holds webcam mode. It returns the port in use via started.
func (c *Client) RunWebcam(ctx context.Context, res, fov int, started func(port int), onFrame func(*image.RGBA)) error {
	ffmpeg := ""
	if onFrame != nil {
		var err error
		if ffmpeg, err = FindFFmpeg(); err != nil {
			diag.Logf("webcam", "ffmpeg not found")
			return errNoFFmpeg
		}
	}
	diag.Logf("webcam", "starting: ip=%s usb=%v res=%d fov=%d inApp=%v", c.IP, IsUSBAddress(c.IP), res, fov, onFrame != nil)
	if IsUSBAddress(c.IP) {
		_, _ = c.get(ctx, "/gopro/camera/control/wired_usb?p=1")
	}
	_, _ = c.get(ctx, "/gopro/webcam/stop")
	usb := IsUSBAddress(c.IP)
	if body, err := c.get(ctx, "/gopro/webcam/version"); err == nil {
		diag.Logf("webcam", "webcam API: %.200s", body)
	}
	port := WebcamPort
	start := fmt.Sprintf("/gopro/webcam/start?res=%d&fov=%d&port=%d&protocol=TS", res, fov, port)
	body, err := c.get(ctx, start)
	if werr, ok := webcamErr(body, usb).(*WebcamError); err != nil && ok && werr.Code == 7 {
		// Seen on a HERO9 that had just connected: "unavailable" once, fine a moment later.
		diag.Logf("webcam", "camera said unavailable (7); retrying once")
		time.Sleep(1500 * time.Millisecond)
		body, err = c.get(ctx, start)
	}
	if err != nil {
		// The camera said why (e.g. error 7: unavailable over Wi-Fi); retrying won't help.
		if werr := webcamErr(body, usb); werr != nil {
			diag.Logf("webcam", "camera refused webcam start: %v", werr)
			return werr
		}
		// Older firmware does not know port/protocol and streams to 8554.
		body2, err2 := c.get(ctx, fmt.Sprintf("/gopro/webcam/start?res=%d&fov=%d", res, fov))
		if err2 != nil {
			diag.Logf("webcam", "both webcam start variants failed: %v / %v", err, err2)
			if werr := webcamErr(body2, usb); werr != nil {
				return werr
			}
			return fmt.Errorf("camera refused webcam mode: %w", err)
		}
		port = 8554
		diag.Logf("webcam", "older firmware: webcam started without port/protocol, streaming to 8554")
	}
	if body, err := c.get(ctx, "/gopro/webcam/status"); err == nil {
		diag.Logf("webcam", "status after start: %.200s", body)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = c.get(ctx, "/gopro/webcam/stop")
		_, _ = c.get(ctx, "/gopro/webcam/exit")
		diag.Logf("webcam", "webcam mode stopped")
	}()
	if started != nil {
		started(port)
	}
	if onFrame == nil {
		<-ctx.Done()
		return nil
	}
	return decodeStream(ctx, ffmpeg, port, onFrame)
}
