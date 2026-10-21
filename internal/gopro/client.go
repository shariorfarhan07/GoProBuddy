// Package gopro talks to a GoPro camera over its Wi-Fi HTTP API.
// See docs/PROTOCOL.md for the endpoints and where each comes from.
package gopro

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"goprobuddy/internal/diag"
)

// DefaultIP is the camera address when the PC is joined to the camera's own access point.
const DefaultIP = "10.5.5.9"

// Client is an HTTP client bound to one camera.
type Client struct {
	IP    string
	http  *http.Client
	mu    sync.Mutex
	model string // cached after the first successful lookup, guarded by mu
}

func NewClient(ip string) *Client {
	if ip == "" {
		ip = DefaultIP
	}
	return &Client{IP: ip, http: &http.Client{Timeout: 8 * time.Second}}
}

func (c *Client) base() string     { return "http://" + c.IP }
func (c *Client) fileBase() string { return "http://" + c.IP + ":8080" }

// quietPaths are polled every few seconds; they are only logged when they fail.
var quietPaths = map[string]bool{
	"/gopro/camera/keep_alive": true,
	"/gopro/camera/state":      true,
	"/gopro/camera/info":       true,
	"/gp/gpControl/info":       true,
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+path, nil)
	if err != nil {
		diag.Logf("http", "GET %s%s: bad request: %v", c.IP, path, err)
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		diag.Logf("http", "GET %s%s failed after %v: %v", c.IP, path, time.Since(start).Round(time.Millisecond), err)
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		diag.Logf("http", "GET %s%s: reading body failed: %v", c.IP, path, err)
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		diag.Logf("http", "GET %s%s -> %s in %v, body: %.300s", c.IP, path, resp.Status, time.Since(start).Round(time.Millisecond), body)
		return body, fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	if p, _, _ := strings.Cut(path, "?"); !quietPaths[p] {
		diag.Logf("http", "GET %s%s -> %d (%d bytes) in %v", c.IP, path, resp.StatusCode, len(body), time.Since(start).Round(time.Millisecond))
	}
	return body, nil
}

// Ping returns nil if the camera answers on its HTTP API.
func (c *Client) Ping(ctx context.Context) error {
	try := func(path string) error {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_, err := c.get(ctx, path)
		return err
	}
	err := try("/gopro/camera/state")
	if err == nil {
		return nil
	}
	// Older cameras only expose the gpControl API.
	err2 := try("/gp/gpControl/status")
	if err2 == nil {
		diag.Logf("camera", "%s answers only the legacy gpControl API", c.IP)
		return nil
	}
	return err
}

// KeepAlive stops the camera from going to sleep while we are connected.
func (c *Client) KeepAlive(ctx context.Context) error {
	_, err := c.get(ctx, "/gopro/camera/keep_alive")
	return err
}

// Status is a small subset of the camera state we display.
type Status struct {
	BatteryPercent int
	Recording      bool
	SDRemainingKB  int64
	NoSDCard       bool
	Model          string
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var st Status
	body, err := c.get(ctx, "/gopro/camera/state")
	if err != nil {
		return st, err
	}
	var raw struct {
		Status map[string]json.RawMessage `json:"status"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		diag.Logf("camera", "state JSON did not parse: %v, body: %.300s", err, body)
		return st, err
	}
	num := func(id string) int64 {
		v, ok := raw.Status[id]
		if !ok {
			return -1
		}
		n, _ := strconv.ParseInt(strings.Trim(string(v), `"`), 10, 64)
		return n
	}
	st.BatteryPercent = int(num("70"))
	st.Recording = num("10") == 1 // 10 = encoding; 8 is only "busy" (e.g. webcam mode)
	st.SDRemainingKB = num("54")
	st.NoSDCard = num("33") == 2 // SD status: 2 = card removed
	var info struct {
		ModelName string `json:"model_name"`
		Info      struct {
			ModelName string `json:"model_name"`
		} `json:"info"`
	}
	c.mu.Lock()
	st.Model = c.model
	c.mu.Unlock()
	if st.Model != "" {
		return st, nil
	}
	// HERO10+ answer /gopro/camera/info; HERO9 only has the gpControl variant.
	for _, p := range []string{"/gopro/camera/info", "/gp/gpControl/info"} {
		if body, err := c.get(ctx, p); err == nil && json.Unmarshal(body, &info) == nil {
			if st.Model = info.ModelName; st.Model == "" {
				st.Model = info.Info.ModelName
			}
			if st.Model != "" {
				c.mu.Lock()
				c.model = st.Model
				c.mu.Unlock()
				break
			}
		}
	}
	return st, nil
}

// SetShutter starts or stops recording.
func (c *Client) SetShutter(ctx context.Context, on bool) error {
	p := "/gopro/camera/shutter/stop"
	if on {
		p = "/gopro/camera/shutter/start"
	}
	_, err := c.get(ctx, p)
	diag.Logf("camera", "shutter on=%v: err=%v", on, err)
	return err
}

// SetTurbo toggles turbo transfer, which speeds up downloads.
func (c *Client) SetTurbo(ctx context.Context, on bool) error {
	p := "0"
	if on {
		p = "1"
	}
	_, err := c.get(ctx, "/gopro/media/turbo_transfer?p="+p)
	diag.Logf("camera", "turbo transfer on=%v: err=%v", on, err)
	return err
}

// MediaItem is one file on the SD card.
type MediaItem struct {
	Dir     string // e.g. 100GOPRO
	Name    string // e.g. GX010001.MP4
	Size    int64
	Created time.Time
}

func (m MediaItem) Path() string { return m.Dir + "/" + m.Name }

func (m MediaItem) IsVideo() bool {
	ext := strings.ToUpper(m.Name[strings.LastIndex(m.Name, ".")+1:])
	return ext == "MP4" || ext == "360" || ext == "MOV"
}

// ListMedia returns every photo and video on the camera.
func (c *Client) ListMedia(ctx context.Context) ([]MediaItem, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	body, err := c.get(ctx, "/gopro/media/list")
	if err != nil {
		var err2 error
		if body, err2 = c.get(ctx, "/gp/gpMediaList"); err2 != nil {
			diag.Logf("media", "media list failed on both APIs: %v / %v", err, err2)
			return nil, err
		}
		diag.Logf("media", "media list came from the legacy gpMediaList API")
	}
	items, err := parseMediaList(body)
	if err != nil {
		diag.Logf("media", "%v, body: %.500s", err, body)
		return nil, err
	}
	var total int64
	for _, it := range items {
		total += it.Size
	}
	diag.Logf("media", "camera lists %d files, %d bytes in total", len(items), total)
	return items, nil
}

func parseMediaList(body []byte) ([]MediaItem, error) {
	var ml struct {
		Media []struct {
			D  string `json:"d"`
			Fs []struct {
				N   string `json:"n"`
				S   string `json:"s"`
				Cre string `json:"cre"`
				B   string `json:"b"` // first index of a group (burst / timelapse)
				L   string `json:"l"` // last index of a group
			} `json:"fs"`
		} `json:"media"`
	}
	if err := json.Unmarshal(body, &ml); err != nil {
		return nil, fmt.Errorf("parse media list: %w", err)
	}
	var out []MediaItem
	for _, d := range ml.Media {
		for _, f := range d.Fs {
			size, _ := strconv.ParseInt(f.S, 10, 64)
			cre, _ := strconv.ParseInt(f.Cre, 10, 64)
			item := MediaItem{Dir: d.D, Name: f.N, Size: size, Created: time.Unix(cre, 0)}
			first, errB := strconv.Atoi(f.B)
			last, errL := strconv.Atoi(f.L)
			dot := strings.LastIndex(f.N, ".")
			if errB != nil || errL != nil || last < first || dot < 4 {
				out = append(out, item)
				continue
			}
			// Groups are listed once; expand to the individual files (prefix + 4-digit index).
			prefix, ext := f.N[:dot-4], f.N[dot:]
			for i := first; i <= last; i++ {
				gi := item
				gi.Name = fmt.Sprintf("%s%04d%s", prefix, i, ext)
				gi.Size = 0 // per-file size is not reported for group members
				out = append(out, gi)
			}
		}
	}
	return out, nil
}

// ThumbnailURL returns a small JPEG preview of a media file.
func (c *Client) ThumbnailURL(m MediaItem) string {
	return c.base() + "/gopro/media/thumbnail?path=" + url.QueryEscape(m.Path())
}

// FileURL is where the full-resolution file is served (port 8080, videos/DCIM).
func (c *Client) FileURL(m MediaItem) string {
	return c.fileBase() + "/videos/DCIM/" + m.Dir + "/" + m.Name
}

// Thumbnail downloads the thumbnail bytes.
func (c *Client) Thumbnail(ctx context.Context, m MediaItem) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ThumbnailURL(m), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		diag.Logf("media", "thumbnail %s: %v", m.Path(), err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		diag.Logf("media", "thumbnail %s: %s", m.Path(), resp.Status)
		return nil, fmt.Errorf("thumbnail: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}
