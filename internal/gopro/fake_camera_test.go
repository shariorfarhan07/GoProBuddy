package gopro

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCamera serves the camera API and file server from one httptest server.
type fakeCamera struct {
	mu     sync.Mutex
	hits   []string
	routes map[string]func(w http.ResponseWriter, r *http.Request)
}

func newFakeCamera(t *testing.T) (*fakeCamera, *Client) {
	fc := &fakeCamera{routes: map[string]func(http.ResponseWriter, *http.Request){}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fc.mu.Lock()
		fc.hits = append(fc.hits, r.URL.RequestURI())
		h := fc.routes[r.URL.Path]
		fc.mu.Unlock()
		if h == nil {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)

	// Client.fileBase appends :8080 and Download uses the default transport,
	// so route every dial to the test server regardless of port.
	addr := srv.Listener.Addr().String()
	old := http.DefaultTransport
	http.DefaultTransport = &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	t.Cleanup(func() { http.DefaultTransport = old })

	host, _, _ := net.SplitHostPort(addr)
	c := NewClient(host)
	c.http.Transport = http.DefaultTransport
	return fc, c
}

func (fc *fakeCamera) handle(path, body string) {
	fc.routes[path] = func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }
}

func TestNewClientDefaultIP(t *testing.T) {
	if NewClient("").IP != DefaultIP || NewClient("1.2.3.4").IP != "1.2.3.4" {
		t.Fatal("NewClient IP handling wrong")
	}
}

func TestPingFallsBackToGpControl(t *testing.T) {
	fc, c := newFakeCamera(t)
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("ping should fail with no endpoints")
	}
	fc.handle("/gp/gpControl/status", "{}")
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping via gpControl: %v", err)
	}
}

func TestStatus(t *testing.T) {
	fc, c := newFakeCamera(t)
	fc.handle("/gopro/camera/state", `{"status":{"70":87,"8":0,"10":1,"54":"123456"}}`)
	fc.handle("/gopro/camera/info", `{"model_name":"HERO12 Black"}`)
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Status{BatteryPercent: 87, Recording: true, SDRemainingKB: 123456, Model: "HERO12 Black"}
	if st != want {
		t.Fatalf("got %+v, want %+v", st, want)
	}
}

func TestStatusHERO9(t *testing.T) {
	fc, c := newFakeCamera(t)
	fc.handle("/gopro/camera/state", `{"status":{"70":98,"33":2,"54":0}}`)
	fc.handle("/gp/gpControl/info", `{"info":{"model_number":55,"model_name":"HERO9 Black"}}`)
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Model != "HERO9 Black" || !st.NoSDCard || st.BatteryPercent != 98 {
		t.Fatalf("got %+v", st)
	}
}

func TestStatusMissingFields(t *testing.T) {
	fc, c := newFakeCamera(t)
	fc.handle("/gopro/camera/state", `{"status":{}}`)
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.BatteryPercent != -1 || st.Recording || st.Model != "" {
		t.Fatalf("unexpected %+v", st)
	}
}

func TestStatusBadJSON(t *testing.T) {
	fc, c := newFakeCamera(t)
	fc.handle("/gopro/camera/state", `not json`)
	if _, err := c.Status(context.Background()); err == nil {
		t.Fatal("want error")
	}
}

func TestCommands(t *testing.T) {
	fc, c := newFakeCamera(t)
	for _, p := range []string{"/gopro/camera/shutter/start", "/gopro/camera/shutter/stop",
		"/gopro/media/turbo_transfer", "/gopro/camera/keep_alive"} {
		fc.handle(p, "{}")
	}
	ctx := context.Background()
	for _, err := range []error{c.SetShutter(ctx, true), c.SetShutter(ctx, false),
		c.SetTurbo(ctx, true), c.SetTurbo(ctx, false), c.KeepAlive(ctx)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"/gopro/camera/shutter/start", "/gopro/camera/shutter/stop",
		"/gopro/media/turbo_transfer?p=1", "/gopro/media/turbo_transfer?p=0", "/gopro/camera/keep_alive"}
	if strings.Join(fc.hits, ",") != strings.Join(want, ",") {
		t.Fatalf("hits = %v", fc.hits)
	}
}

func TestListMediaFallback(t *testing.T) {
	fc, c := newFakeCamera(t)
	fc.handle("/gp/gpMediaList", `{"media":[{"d":"100GOPRO","fs":[{"n":"GOPR0001.JPG","s":"10"}]}]}`)
	items, err := c.ListMedia(context.Background())
	if err != nil || len(items) != 1 || items[0].Path() != "100GOPRO/GOPR0001.JPG" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestParseMediaListEdgeCases(t *testing.T) {
	if _, err := parseMediaList([]byte("{")); err == nil {
		t.Error("want error on bad JSON")
	}
	items, err := parseMediaList([]byte(`{"media":[{"d":"D","fs":[
		{"n":"A.JPG","b":"5","l":"3"},
		{"n":"X.JPG","b":"1","l":"2"}
	]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	// l<b and too-short names must not be expanded.
	if len(items) != 2 || items[0].Name != "A.JPG" || items[1].Name != "X.JPG" {
		t.Fatalf("got %+v", items)
	}
}

func TestIsVideo(t *testing.T) {
	cases := map[string]bool{"a.MP4": true, "a.mp4": true, "a.360": true, "a.MOV": true,
		"a.JPG": false, "a.GPR": false, "noext": false}
	for n, want := range cases {
		if got := (MediaItem{Name: n}).IsVideo(); got != want {
			t.Errorf("%s: got %v", n, got)
		}
	}
}

func TestThumbnail(t *testing.T) {
	fc, c := newFakeCamera(t)
	m := MediaItem{Dir: "100GOPRO", Name: "G 1&.JPG"}
	fc.routes["/gopro/media/thumbnail"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("path") != m.Path() {
			http.Error(w, "bad path", 400)
			return
		}
		w.Write([]byte("jpeg"))
	}
	b, err := c.Thumbnail(context.Background(), m)
	if err != nil || string(b) != "jpeg" {
		t.Fatalf("b=%q err=%v", b, err)
	}
	if _, err := c.Thumbnail(context.Background(), MediaItem{Dir: "x", Name: "y"}); err == nil {
		t.Fatal("want error for bad path")
	}
}

const payload = "0123456789abcdefghijklmnopqrstuvwxyz"

func serveFile(fc *fakeCamera, honorRange bool) {
	fc.routes["/videos/DCIM/100GOPRO/GX01.MP4"] = func(w http.ResponseWriter, r *http.Request) {
		if rg := r.Header.Get("Range"); honorRange && rg != "" {
			off, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)-off))
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte(payload[off:]))
			return
		}
		w.Write([]byte(payload))
	}
}

func TestDownloadFresh(t *testing.T) {
	fc, c := newFakeCamera(t)
	serveFile(fc, true)
	dir := t.TempDir()
	created := time.Unix(1696000000, 0)
	m := MediaItem{Dir: "100GOPRO", Name: "GX01.MP4", Created: created}
	var last, lastTotal int64
	p, err := c.Download(context.Background(), m, filepath.Join(dir, "sub"), func(d, tot int64) { last, lastTotal = d, tot })
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != payload || last != int64(len(payload)) || lastTotal != int64(len(payload)) {
		t.Fatalf("content=%q last=%d total=%d", b, last, lastTotal)
	}
	if fi, _ := os.Stat(p); !fi.ModTime().Equal(created) {
		t.Errorf("mtime = %v", fi.ModTime())
	}
	if _, err := os.Stat(p + ".part"); !os.IsNotExist(err) {
		t.Error(".part left behind")
	}
}

func TestDownloadResume(t *testing.T) {
	for _, honor := range []bool{true, false} {
		fc, c := newFakeCamera(t)
		serveFile(fc, honor)
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "GX01.MP4.part"), []byte(payload[:10]), 0o644)
		m := MediaItem{Dir: "100GOPRO", Name: "GX01.MP4", Size: int64(len(payload))}
		p, err := c.Download(context.Background(), m, dir, func(int64, int64) {})
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(p); string(b) != payload {
			t.Fatalf("honorRange=%v content=%q", honor, b)
		}
	}
}

func TestDownloadSkipsExisting(t *testing.T) {
	fc, c := newFakeCamera(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "GX01.MP4"), []byte(payload), 0o644)
	m := MediaItem{Dir: "100GOPRO", Name: "GX01.MP4", Size: int64(len(payload))}
	if _, err := c.Download(context.Background(), m, dir, func(int64, int64) {}); err != nil {
		t.Fatal(err)
	}
	if len(fc.hits) != 0 {
		t.Fatalf("should not hit network: %v", fc.hits)
	}
}

func TestDownloadHTTPError(t *testing.T) {
	_, c := newFakeCamera(t)
	m := MediaItem{Dir: "100GOPRO", Name: "missing.MP4"}
	if _, err := c.Download(context.Background(), m, t.TempDir(), func(int64, int64) {}); err == nil {
		t.Fatal("want error on 404")
	}
}

func TestStreamStartFallbacks(t *testing.T) {
	fc, c := newFakeCamera(t)
	ctx := context.Background()
	if _, err := c.startStream(ctx); err == nil {
		t.Fatal("want error when no stream endpoint")
	}
	fc.handle("/gp/gpExec", "{}")
	if port, err := c.startStream(ctx); err != nil || port != 8554 {
		t.Fatalf("port=%d err=%v", port, err)
	}
	fc.handle("/gopro/camera/stream/start", "{}")
	if port, err := c.startStream(ctx); err != nil || port != 8556 {
		t.Fatalf("port=%d err=%v", port, err)
	}
}
