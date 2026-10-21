package gopro

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"goprobuddy/internal/diag"
)

// Preview frame size handed to the UI. ffmpeg letterboxes the camera image into it.
const (
	PreviewW = 960
	PreviewH = 540
)

// errNoFFmpeg is returned when no ffmpeg binary can be found.
var errNoFFmpeg = errors.New("ffmpeg not found: install it (Windows: winget install Gyan.FFmpeg, macOS: brew install ffmpeg, Linux: your package manager) or put it next to GoProBuddy")

// FindFFmpeg looks next to the executable first (and in a macOS app bundle's Resources),
// then on PATH, then in common install folders. Apps started from the macOS Finder or a
// Linux desktop launcher don't get the shell's PATH, so Homebrew's folders are checked too.
func FindFFmpeg() (string, error) {
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name = "ffmpeg.exe"
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, name), filepath.Join(dir, "..", "Resources", name))
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	candidates = append(candidates, "/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg", "/usr/bin/ffmpeg", "/snap/bin/ffmpeg")
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", errNoFFmpeg
}

// startStream asks the camera to start the UDP preview and returns the local port it streams to.
func (c *Client) startStream(ctx context.Context) (int, error) {
	// Current API (HERO9+): stream to port 8556.
	_, _ = c.get(ctx, "/gopro/camera/stream/stop")
	if _, err := c.get(ctx, "/gopro/camera/stream/start?port=8556"); err == nil {
		diag.Logf("stream", "preview started with the current API on port 8556")
		return 8556, nil
	}
	// Older gpControl cameras stream to 8554.
	if _, err := c.get(ctx, "/gp/gpControl/execute?p1=gpStream&a1=proto_v2&c1=restart"); err == nil {
		return 8554, nil
	}
	if _, err := c.get(ctx, "/gp/gpExec?p1=gpStreamA9&c1=restart"); err == nil {
		return 8554, nil
	}
	diag.Logf("stream", "every preview start endpoint failed")
	return 0, errors.New("camera refused to start the preview stream")
}

func (c *Client) stopStream() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.get(ctx, "/gopro/camera/stream/stop"); err != nil {
		_, _ = c.get(ctx, "/gp/gpExec?p1=gpStreamA9&c1=stop")
	}
}

// RunPreview starts the live preview and calls onFrame for every decoded frame
// until ctx is cancelled. The *image.RGBA passed to onFrame is owned by the callee.
func (c *Client) RunPreview(ctx context.Context, onFrame func(*image.RGBA)) error {
	ffmpeg, err := FindFFmpeg()
	if err != nil {
		diag.Logf("stream", "ffmpeg not found next to the exe or on PATH")
		return errNoFFmpeg
	}
	diag.Logf("stream", "using ffmpeg at %s", ffmpeg)
	port, err := c.startStream(ctx)
	if err != nil {
		return err
	}
	defer c.stopStream()

	// The camera stops streaming unless it receives this datagram on 8554 every few seconds.
	go func() {
		conn, err := net.Dial("udp", net.JoinHostPort(c.IP, "8554"))
		if err != nil {
			return
		}
		defer conn.Close()
		msg := []byte("_GPHD_:0:0:2:0.000000\n")
		t := time.NewTicker(2500 * time.Millisecond)
		defer t.Stop()
		for {
			_, _ = conn.Write(msg)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	return decodeStream(ctx, ffmpeg, port, onFrame)
}

// decodeStream decodes the MPEG-TS the camera sends to a local UDP port and calls onFrame for
// every frame until ctx is cancelled.
func decodeStream(ctx context.Context, ffmpeg string, port int, onFrame func(*image.RGBA)) error {
	vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2",
		PreviewW, PreviewH, PreviewW, PreviewH)
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "warning",
		"-fflags", "nobuffer", "-flags", "low_delay",
		"-probesize", "500000", "-analyzeduration", "500000",
		"-f", "mpegts",
		"-i", "udp://0.0.0.0:"+strconv.Itoa(port)+"?overrun_nonfatal=1&fifo_size=1000000",
		"-an", "-vf", vf, "-pix_fmt", "rgba", "-f", "rawvideo", "pipe:1")
	hideWindow(cmd)
	cmd.Stderr = diag.Writer("ffmpeg")
	diag.Logf("stream", "decoding udp port %d: %s", port, strings.Join(cmd.Args, " "))
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		diag.Logf("stream", "ffmpeg did not start: %v", err)
		return err
	}
	defer func() { diag.Logf("stream", "ffmpeg exited: %v", cmd.Wait()) }()
	frames := 0

	r := bufio.NewReaderSize(out, PreviewW*PreviewH*4)
	for {
		img := image.NewRGBA(image.Rect(0, 0, PreviewW, PreviewH))
		if _, err := io.ReadFull(r, img.Pix); err != nil {
			diag.Logf("stream", "stream ended after %d frames: %v", frames, err)
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("preview stream ended: %w", err)
		}
		if frames++; frames == 1 {
			diag.Logf("stream", "first frame decoded")
		}
		onFrame(img)
	}
}
