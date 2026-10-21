// GoProBuddy is a native desktop app that connects to a GoPro, downloads its
// photos and videos, and shows the live preview.
package main

//go:generate go run ./tools/winres assets/logo.ico

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"goprobuddy/internal/diag"
	"goprobuddy/internal/gopro"
)

// appVersion is set at build time with -ldflags "-X main.appVersion=1.2.3".
var appVersion = "dev"

var (
	colBg      = color.NRGBA{R: 0x08, G: 0x0B, B: 0x0E, A: 0xFF}
	colSidebar = color.NRGBA{R: 0x0D, G: 0x10, B: 0x14, A: 0xFF}
	colPanel   = color.NRGBA{R: 0x11, G: 0x15, B: 0x1A, A: 0xFF}
	colRow     = color.NRGBA{R: 0x18, G: 0x1D, B: 0x23, A: 0xFF}
	colLine    = color.NRGBA{R: 0x24, G: 0x2A, B: 0x32, A: 0xFF}
	colAccent  = color.NRGBA{R: 0x13, G: 0xBC, B: 0xF2, A: 0xFF}
	colMuted   = color.NRGBA{R: 0x7F, G: 0x8B, B: 0x9A, A: 0xFF}
	colText    = color.NRGBA{R: 0xEC, G: 0xEF, B: 0xF4, A: 0xFF}
	colRed     = color.NRGBA{R: 0xE5, G: 0x48, B: 0x4D, A: 0xFF}
	colGreen   = color.NRGBA{R: 0x32, G: 0xCD, B: 0x32, A: 0xFF} // #32CD32
	colThumb   = color.NRGBA{R: 0x1E, G: 0x24, B: 0x2B, A: 0xFF}
)

// Sidebar pages.
const (
	pageMedia = iota
	pageDownloads
	pagePreview
	pageWebcam
	pageSettings
	pageConnection
	pageActivity
	pageCount
)

var pageNames = [pageCount]string{"Camera media", "Downloads", "Live preview", "Webcam", "Camera settings", "Camera connection", "Activity"}

// Media filters.
const (
	filterAll = iota
	filterVideos
	filterPhotos
	filterNotSaved
	filterCount
)

type mediaRow struct {
	item     gopro.MediaItem
	selected bool
	tile     widget.Clickable
}

type settingRow struct {
	gopro.Setting
	open    widget.Clickable
	options []widget.Clickable
}

type foundRow struct {
	cam     gopro.FoundCamera
	connect widget.Clickable
}

// App holds all UI and connection state. Fields below mu are shared with background goroutines.
type App struct {
	win *app.Window
	th  *material.Theme

	// widgets (UI goroutine only)
	scanBtn, wifiBtn, recordBtn  widget.Clickable
	navBtns                      [pageCount]widget.Clickable
	filterBtns                   [filterCount]widget.Clickable
	refreshBtn, selectAllBtn     widget.Clickable
	downloadBtn, cancelBtn       widget.Clickable
	openFolderBtn, previewBtn    widget.Clickable
	clearBtn, emptyScanBtn       widget.Clickable
	findCamBtn, usbBtn           widget.Clickable
	diagBtn, diagFolderBtn       widget.Clickable
	webcamBtn, reloadSetBtn      widget.Clickable
	resBtns, fovBtns             []widget.Clickable
	webcamRes, webcamFOV         int // indexes into gopro.WebcamResolutions / WebcamFOVs
	webcamExternal               bool
	webcamModeBtns               [2]widget.Clickable
	settingsList                 widget.List
	openSetting                  int // id of the expanded setting row, 0 for none
	ipEditor, folderEditor       widget.Editor
	searchEditor                 widget.Editor
	mediaList, foundList, logLst widget.List
	dlList                       widget.List
	page, filter                 int
	visible                      []*mediaRow // media rows after filter/search, as last drawn
	lastFolder                   string      // save folder, once typing has settled (guarded by mu)
	typedFolder                  string
	folderTimer                  *time.Timer

	mu           sync.Mutex
	client       *gopro.Client
	ble          *gopro.BLECamera
	connected    bool
	busy         string // non-empty while a connect/scan is running
	status       gopro.Status
	found        []*foundRow
	media        []*mediaRow
	thumbs       map[string]paint.ImageOp
	thumbReq     map[string]bool
	thumbSem     chan struct{}
	logs         []string
	onDisk       map[string]bool // file names already in the save folder
	saved        []string        // files saved this session, newest last
	dlActive     bool
	dlCancel     context.CancelFunc
	dlName       string
	dlDone       int64
	dlTotal      int64
	dlIndex      int
	dlCount      int
	previewOn    bool   // a stream (preview or webcam) is running
	streamKind   string // "preview" or "webcam"
	streamPort   int
	settings     []*settingRow
	setVals      map[int]int
	setStatus    map[int]int
	setFilters   []gopro.Filter
	usbIP        string // camera address on a USB-C link, "" if none (polled)
	settingsBusy bool
	setErr       string
	previewCnl   context.CancelFunc
	frame        *image.RGBA
	frameOp      paint.ImageOp
	frameNew     bool
	stopKeep     context.CancelFunc
}

func main() {
	log.SetOutput(io.MultiWriter(os.Stderr, diag.Writer("go")))
	if diag.WantEnabled() {
		_, _ = diag.Enable(appInfo())
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				diag.Logf("panic", "%v\n%s", r, debug.Stack())
				panic(r)
			}
		}()
		w := new(app.Window)
		w.Option(app.Title("GoProBuddy"), app.Size(unit.Dp(1240), unit.Dp(780)), app.MinSize(unit.Dp(900), unit.Dp(560)))
		if err := newApp(w).run(); err != nil {
			log.Fatal(err)
		}
		diag.Logf("app", "window closed, exiting")
		os.Exit(0)
	}()
	app.Main()
}

func appInfo() string {
	exe, _ := os.Executable()
	return fmt.Sprintf("GoProBuddy %s  (%s)", appVersion, exe)
}

func newApp(w *app.Window) *App {
	th := material.NewTheme()
	th.Palette = material.Palette{Bg: colBg, Fg: colText, ContrastBg: colAccent, ContrastFg: color.NRGBA{A: 0xFF, R: 0xFF, G: 0xFF, B: 0xFF}}
	a := &App{
		win:      w,
		th:       th,
		thumbs:   map[string]paint.ImageOp{},
		thumbReq: map[string]bool{},
		thumbSem: make(chan struct{}, 3),
		onDisk:   map[string]bool{},
	}
	a.searchEditor.SingleLine = true
	a.settingsList.Axis = layout.Vertical
	a.resBtns = make([]widget.Clickable, len(gopro.WebcamResolutions))
	a.fovBtns = make([]widget.Clickable, len(gopro.WebcamFOVs))
	a.dlList.Axis = layout.Vertical
	a.ipEditor.SingleLine = true
	a.ipEditor.SetText(gopro.DefaultIP)
	a.folderEditor.SingleLine = true
	home, _ := os.UserHomeDir()
	a.folderEditor.SetText(filepath.Join(home, "Videos", "GoPro"))
	a.mediaList.Axis = layout.Vertical
	a.foundList.Axis = layout.Vertical
	a.logLst.Axis = layout.Vertical
	a.logLst.ScrollToEnd = true
	diag.Logf("app", "%s starting", appInfo())
	go a.watchUSB()
	a.logf("Welcome. Find your camera over Bluetooth, or join its Wi-Fi and connect by IP.")
	return a
}

func (a *App) run() error {
	var ops op.Ops
	for {
		switch e := a.win.Event().(type) {
		case app.DestroyEvent:
			a.shutdown()
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			a.handleInput(gtx)
			a.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (a *App) shutdown() {
	diag.Logf("app", "shutting down")
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.previewCnl != nil {
		a.previewCnl()
	}
	if a.dlCancel != nil {
		a.dlCancel()
	}
	if a.stopKeep != nil {
		a.stopKeep()
	}
	if a.ble != nil {
		_ = a.ble.Close()
	}
}

// logf shows a message in Activity and writes it to the diagnostic log.
func (a *App) logf(format string, args ...any) {
	diag.Logf("app", format, args...)
	a.showf(format, args...)
}

// showf shows a message in Activity only. Use it for text the log file must not contain.
func (a *App) showf(format string, args ...any) {
	msg := time.Now().Format("15:04:05  ") + fmt.Sprintf(format, args...)
	a.mu.Lock()
	a.logs = append(a.logs, msg)
	if len(a.logs) > 200 {
		a.logs = a.logs[len(a.logs)-200:]
	}
	a.mu.Unlock()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// ---------- actions ----------

func (a *App) handleInput(gtx layout.Context) {
	if a.diagBtn.Clicked(gtx) {
		if diag.Enabled() {
			a.logf("Diagnostic logging switched off. The log file is kept in %s.", diag.Dir())
			diag.Disable()
		} else if p, err := diag.Enable(appInfo()); err != nil {
			a.logf("Could not start the diagnostic log: %v", err)
		} else {
			a.logf("Diagnostic logging on. Writing to %s", p)
			a.logState()
		}
	}
	if a.diagFolderBtn.Clicked(gtx) {
		_ = os.MkdirAll(diag.Dir(), 0o755)
		if p := diag.Path(); p != "" {
			revealFile(p)
		} else {
			openFolder(diag.Dir())
		}
	}
	if a.scanBtn.Clicked(gtx) || a.emptyScanBtn.Clicked(gtx) || a.findCamBtn.Clicked(gtx) {
		a.page = pageConnection
		go a.scan()
	}
	for i := range a.navBtns {
		if a.navBtns[i].Clicked(gtx) {
			diag.Logf("ui", "open page %q", pageNames[i])
			a.page = i
			if i == pageSettings {
				a.mu.Lock()
				load := a.settings == nil && !a.settingsBusy && a.connected
				a.mu.Unlock()
				if load {
					go a.loadSettings()
				}
			}
		}
	}
	if a.reloadSetBtn.Clicked(gtx) {
		go a.loadSettings()
	}
	if a.usbBtn.Clicked(gtx) {
		go a.connectUSB()
	}
	if a.webcamBtn.Clicked(gtx) {
		a.toggleStream("webcam")
	}
	for i := range a.resBtns {
		if a.resBtns[i].Clicked(gtx) {
			a.webcamRes = i
		}
	}
	for i := range a.fovBtns {
		if a.fovBtns[i].Clicked(gtx) {
			a.webcamFOV = i
		}
	}
	for i := range a.webcamModeBtns {
		if a.webcamModeBtns[i].Clicked(gtx) {
			a.webcamExternal = i == 1
		}
	}
	a.mu.Lock()
	setRows := a.settings
	a.mu.Unlock()
	for _, r := range setRows {
		if r.open.Clicked(gtx) {
			if a.openSetting == r.ID {
				a.openSetting = 0
			} else {
				a.openSetting = r.ID
			}
		}
		for i := range r.options {
			if r.options[i].Clicked(gtx) {
				go a.applySetting(r.ID, r.Name, r.Options[i])
			}
		}
	}
	for i := range a.filterBtns {
		if a.filterBtns[i].Clicked(gtx) {
			a.filter = i
			diag.Logf("ui", "media filter %d", i)
		}
	}
	// Act on the save folder once typing pauses, not on every keystroke.
	if dir := a.folderEditor.Text(); dir != a.typedFolder {
		a.typedFolder = dir
		if a.folderTimer != nil {
			a.folderTimer.Stop()
		}
		a.folderTimer = time.AfterFunc(600*time.Millisecond, func() {
			a.mu.Lock()
			a.lastFolder = dir
			a.mu.Unlock()
			diag.Logf("ui", "save folder is %q", dir)
			a.scanDisk()
		})
	}
	if a.wifiBtn.Clicked(gtx) {
		ip := a.ipEditor.Text()
		go a.connectWiFi(ip)
	}
	if a.recordBtn.Clicked(gtx) {
		go a.toggleRecord()
	}
	if a.refreshBtn.Clicked(gtx) {
		go a.refreshMedia()
	}
	if a.previewBtn.Clicked(gtx) {
		a.toggleStream("preview")
	}
	if a.openFolderBtn.Clicked(gtx) {
		dir := a.folderEditor.Text()
		_ = os.MkdirAll(dir, 0o755)
		openFolder(dir)
	}
	if a.cancelBtn.Clicked(gtx) {
		a.mu.Lock()
		if a.dlCancel != nil {
			a.dlCancel()
		}
		a.mu.Unlock()
	}

	a.mu.Lock()
	rows := a.media
	found := a.found
	onDisk := a.onDisk
	a.mu.Unlock()

	if a.selectAllBtn.Clicked(gtx) {
		all := len(a.visible) > 0
		for _, r := range a.visible {
			all = all && r.selected
		}
		for _, r := range a.visible {
			r.selected = !all
		}
	}
	if a.clearBtn.Clicked(gtx) {
		for _, r := range rows {
			r.selected = false
		}
	}
	for _, r := range rows {
		if r.tile.Clicked(gtx) {
			r.selected = !r.selected
		}
	}
	var queue []gopro.MediaItem
	if a.downloadBtn.Clicked(gtx) {
		// Selected files if any; otherwise everything not yet saved.
		for _, r := range rows {
			if r.selected {
				queue = append(queue, r.item)
			}
		}
		if len(queue) == 0 {
			for _, r := range rows {
				if !onDisk[r.item.Name] {
					queue = append(queue, r.item)
				}
			}
		}
		if len(queue) == 0 {
			a.logf("Everything on the camera is already saved.")
		}
	}
	if len(queue) > 0 {
		a.startDownloads(queue, a.folderEditor.Text())
	}
	for _, f := range found {
		if f.connect.Clicked(gtx) {
			go a.connectBLE(f.cam)
		}
	}
}

func (a *App) setBusy(s string) {
	a.mu.Lock()
	a.busy = s
	a.mu.Unlock()
	a.win.Invalidate()
}

func (a *App) scan() {
	a.mu.Lock()
	if a.busy != "" {
		a.mu.Unlock()
		return
	}
	a.busy = "Scanning…"
	a.found = nil
	a.mu.Unlock()
	defer a.setBusy("")
	a.logf("Scanning for GoPro cameras over Bluetooth (10 s)…")
	err := gopro.Scan(10*time.Second, func(c gopro.FoundCamera) {
		a.mu.Lock()
		a.found = append(a.found, &foundRow{cam: c})
		a.mu.Unlock()
		a.logf("Found %s (signal %d dBm)", c.Name, c.RSSI)
	})
	if err != nil {
		a.logf("Scan failed: %v", err)
		return
	}
	a.mu.Lock()
	n := len(a.found)
	a.mu.Unlock()
	if n == 0 {
		a.logf("No camera found. Turn the camera on; for first use put it in pairing mode (Preferences → Wireless → Connect Device).")
	}
}

func (a *App) connectBLE(cam gopro.FoundCamera) {
	a.mu.Lock()
	if a.busy != "" {
		a.mu.Unlock()
		return
	}
	a.busy = "Connecting…"
	a.mu.Unlock()
	defer a.setBusy("")

	// Reuse an open link to the same camera: connecting a second time while the first
	// link is up makes Windows return the services without their characteristics.
	a.mu.Lock()
	b := a.ble
	a.mu.Unlock()
	if b != nil && b.Address == cam.Address {
		a.logf("Already linked to %s over Bluetooth.", cam.Name)
	} else {
		a.logf("Connecting to %s over Bluetooth…", cam.Name)
		nb, err := gopro.Connect(cam)
		if err != nil {
			a.logf("Bluetooth connect failed: %v", err)
			a.logf("Tip: pair the camera once in Windows Settings → Bluetooth while it is in pairing mode, then try again.")
			return
		}
		a.mu.Lock()
		if a.ble != nil {
			_ = a.ble.Close()
		}
		a.ble, b = nb, nb
		a.mu.Unlock()
	}

	a.logf("Turning on camera Wi-Fi…")
	if err := b.EnableWiFi(); err != nil {
		a.logf("%v", err)
		return
	}
	ssid, pass, err := b.WiFiCredentials()
	if err != nil {
		a.logf("%v", err)
		return
	}
	a.logf("Camera Wi-Fi: %s — joining…", ssid)
	if err := gopro.JoinWiFi(ssid, pass); err != nil {
		a.logf("Could not join Wi-Fi automatically: %v", err)
		diag.Logf("app", "asked user to join %q manually (password %s)", ssid, diag.Redact(pass))
		a.showf("Join \"%s\" manually (password: %s), then press Connect under \"Already on camera Wi-Fi?\".", ssid, pass)
		return
	}
	a.connectWiFi(gopro.DefaultIP)
}

func (a *App) connectWiFi(ip string) {
	c := gopro.NewClient(ip)
	a.logf("Contacting camera at %s…", c.IP)
	var err error
	for i := 0; i < 15; i++ { // Wi-Fi association can take a few seconds
		if err = c.Ping(context.Background()); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		a.logf("Camera not reachable at %s: %v (PC Wi-Fi: %s)", c.IP, err, gopro.WiFiState())
		if c.IP == gopro.DefaultIP {
			a.logf("Make sure this PC is joined to the camera's Wi-Fi network, then try again.")
		}
		return
	}
	a.mu.Lock()
	a.client = c
	a.connected = true
	if a.stopKeep != nil {
		a.stopKeep()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.stopKeep = cancel
	a.mu.Unlock()
	a.logf("Connected to camera.")
	go a.keepAlive(ctx, c)
	a.refreshMedia()
}

// keepAlive stops the camera sleeping and refreshes the status panel.
func (a *App) keepAlive(ctx context.Context, c *gopro.Client) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	fails := 0
	for {
		_ = c.KeepAlive(ctx)
		a.mu.Lock()
		b := a.ble
		a.mu.Unlock()
		if b != nil {
			_ = b.KeepAlive()
		}
		st, err := c.Status(ctx)
		a.mu.Lock()
		if err == nil {
			if st != a.status {
				diag.Logf("camera", "status: model=%q battery=%d%% recording=%v sdFreeKB=%d noSD=%v", st.Model, st.BatteryPercent, st.Recording, st.SDRemainingKB, st.NoSDCard)
			}
			if fails > 0 {
				diag.Logf("camera", "camera answering again after %d failed polls", fails)
			}
			a.status, fails = st, 0
			a.connected = true
		} else if fails++; fails < 4 {
			diag.Logf("camera", "status poll %d failed: %v", fails, err)
		} else if a.connected {
			a.connected = false
			a.mu.Unlock()
			a.logf("Lost contact with camera.")
			a.mu.Lock()
		}
		a.mu.Unlock()
		a.win.Invalidate()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *App) currentClient() *gopro.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.connected {
		return nil
	}
	return a.client
}

func (a *App) toggleRecord() {
	c := a.currentClient()
	if c == nil {
		a.logf("Not connected.")
		return
	}
	a.mu.Lock()
	rec := a.status.Recording
	a.mu.Unlock()
	if err := c.SetShutter(context.Background(), !rec); err != nil {
		a.logf("Shutter: %v", err)
		return
	}
	a.mu.Lock()
	a.status.Recording = !rec
	a.mu.Unlock()
	a.win.Invalidate()
	if rec {
		a.logf("Recording stopped.")
		time.Sleep(1500 * time.Millisecond)
		a.refreshMedia()
	} else {
		a.logf("Recording started.")
	}
}

func (a *App) refreshMedia() {
	c := a.currentClient()
	if c == nil {
		a.logf("Not connected.")
		return
	}
	items, err := c.ListMedia(context.Background())
	if err != nil {
		if st, serr := c.Status(context.Background()); serr == nil && st.NoSDCard {
			a.mu.Lock()
			a.status, a.media = st, nil
			a.mu.Unlock()
			a.logf("No SD card in the camera. Insert one to see and download media.")
			return
		}
		a.logf("Could not list media: %v", err)
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Created.After(items[j].Created) })
	rows := make([]*mediaRow, len(items))
	for i, it := range items {
		rows[i] = &mediaRow{item: it}
	}
	a.mu.Lock()
	a.media = rows
	a.mu.Unlock()
	a.logf("%d files on camera.", len(items))
	a.scanDisk()
}

// scanDisk records which camera files already exist, complete, in the save folder.
func (a *App) scanDisk() {
	a.mu.Lock()
	dir, rows := a.lastFolder, a.media
	a.mu.Unlock()
	if dir == "" {
		return
	}
	got := map[string]bool{}
	for _, r := range rows {
		if fi, err := os.Stat(filepath.Join(dir, r.item.Name)); err == nil && (r.item.Size == 0 || fi.Size() == r.item.Size) {
			got[r.item.Name] = true
		}
	}
	a.mu.Lock()
	a.onDisk = got
	a.mu.Unlock()
	a.win.Invalidate()
}

func (a *App) requestThumb(m gopro.MediaItem) {
	key := m.Path()
	a.mu.Lock()
	if a.thumbReq[key] || a.client == nil {
		a.mu.Unlock()
		return
	}
	a.thumbReq[key] = true
	c := a.client
	a.mu.Unlock()
	go func() {
		a.thumbSem <- struct{}{}
		defer func() { <-a.thumbSem }()
		data, err := c.Thumbnail(context.Background(), m)
		if err != nil {
			return
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return
		}
		a.mu.Lock()
		a.thumbs[key] = paint.NewImageOp(img)
		a.mu.Unlock()
		a.win.Invalidate()
	}()
}

func (a *App) startDownloads(items []gopro.MediaItem, dir string) {
	c := a.currentClient()
	if c == nil {
		a.logf("Not connected.")
		return
	}
	a.mu.Lock()
	if a.dlActive {
		a.mu.Unlock()
		a.logf("A download is already running.")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.dlActive, a.dlCancel, a.dlCount = true, cancel, len(items)
	for _, it := range items {
		diag.Logf("download", "queued %s (%d bytes)", it.Path(), it.Size)
	}
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.dlActive, a.dlCancel = false, nil
			a.mu.Unlock()
			a.win.Invalidate()
		}()
		_ = c.SetTurbo(ctx, true)
		defer c.SetTurbo(context.Background(), false)
		a.logf("Downloading %d file(s) to %s", len(items), dir)
		ok := 0
		last := time.Time{}
		for i, it := range items {
			a.mu.Lock()
			a.dlIndex, a.dlName, a.dlDone, a.dlTotal = i+1, it.Name, 0, it.Size
			a.mu.Unlock()
			_, err := c.Download(ctx, it, dir, func(done, total int64) {
				a.mu.Lock()
				a.dlDone, a.dlTotal = done, total
				a.mu.Unlock()
				if time.Since(last) > 100*time.Millisecond {
					last = time.Now()
					a.win.Invalidate()
				}
			})
			if ctx.Err() != nil {
				a.logf("Download cancelled (partial file kept, it will resume next time).")
				return
			}
			if err != nil {
				a.logf("%s: %v", it.Name, err)
				continue
			}
			ok++
			a.mu.Lock()
			a.onDisk[it.Name] = true
			a.saved = append(a.saved, it.Name)
			a.mu.Unlock()
		}
		a.logf("Download finished: %d of %d file(s) saved.", ok, len(items))
	}()
}

// toggleStream starts or stops the live preview or webcam stream. Only one runs at a time,
// since both arrive on the same UDP port.
func (a *App) toggleStream(kind string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.previewOn {
		if a.previewCnl != nil {
			a.previewCnl()
		}
		if a.streamKind != kind {
			go a.logf("Stopped the %s; press start again once it has closed.", a.streamKind)
		}
		return
	}
	if !a.connected || a.client == nil {
		go a.logf("Not connected.")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.previewOn, a.previewCnl, a.streamKind, a.streamPort = true, cancel, kind, 0
	c := a.client
	onFrame := func(img *image.RGBA) {
		a.mu.Lock()
		a.frame, a.frameNew = img, true
		a.mu.Unlock()
		a.win.Invalidate()
	}
	if kind == "webcam" && !gopro.IsUSBAddress(c.IP) {
		if usb := gopro.FindUSBCamera(); usb != "" {
			go a.logf("Using the USB-C connection (%s) for webcam mode.", usb)
			c = gopro.NewClient(usb)
		}
	}
	res, fov := gopro.WebcamResolutions[a.webcamRes], gopro.WebcamFOVs[a.webcamFOV]
	external := a.webcamExternal
	go func() {
		var err error
		name := "Preview"
		if kind == "webcam" {
			name = "Webcam"
			a.logf("Starting webcam mode (%s, %s)…", res.Name, fov.Name)
			cb := onFrame
			if external {
				cb = nil
			}
			err = c.RunWebcam(ctx, res.Value, fov.Value, func(port int) {
				a.mu.Lock()
				a.streamPort = port
				a.mu.Unlock()
				if external {
					a.logf("Webcam is streaming to udp://@:%d. Open that in OBS (Media Source) or VLC.", port)
				}
				a.win.Invalidate()
			}, cb)
		} else {
			a.logf("Starting live preview…")
			err = c.RunPreview(ctx, onFrame)
		}
		a.mu.Lock()
		a.previewOn, a.previewCnl, a.frame = false, nil, nil
		a.mu.Unlock()
		if err != nil {
			a.logf("%s: %v", name, err)
		} else {
			a.logf("%s stopped.", name)
		}
		a.win.Invalidate()
	}()
}

func (a *App) connectUSB() {
	ip := gopro.FindUSBCamera()
	if ip == "" {
		a.logf("No GoPro found on USB. Plug it in with USB-C and set the camera's USB connection to GoPro Connect (Preferences → Connections → USB Connection).")
		return
	}
	a.logf("GoPro found on USB at %s.", ip)
	a.connectWiFi(ip)
}

func (a *App) loadSettings() {
	c := a.currentClient()
	if c == nil {
		a.logf("Not connected.")
		return
	}
	a.mu.Lock()
	if a.settingsBusy {
		a.mu.Unlock()
		return
	}
	a.settingsBusy, a.setErr = true, ""
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.settingsBusy = false
		a.mu.Unlock()
		a.win.Invalidate()
	}()
	list, filters, err := c.Settings(context.Background())
	if err != nil {
		a.mu.Lock()
		a.setErr = err.Error()
		a.mu.Unlock()
		a.logf("Could not read camera settings: %v", err)
		return
	}
	status, vals, _ := c.CameraState(context.Background())
	rows := make([]*settingRow, len(list))
	for i, s := range list {
		rows[i] = &settingRow{Setting: s, options: make([]widget.Clickable, len(s.Options))}
	}
	a.mu.Lock()
	a.settings, a.setVals, a.setStatus, a.setFilters = rows, vals, status, filters
	a.mu.Unlock()
	a.logf("Loaded %d camera settings.", len(rows))
}

func (a *App) applySetting(id int, name string, opt gopro.SettingOption) {
	c := a.currentClient()
	if c == nil {
		a.logf("Not connected.")
		return
	}
	a.mu.Lock()
	cur, ok := a.setVals[id]
	a.mu.Unlock()
	if ok && cur == opt.Value {
		return // already set; the camera answers a no-op change with an error
	}
	if err := c.SetSetting(context.Background(), id, opt.Value); err != nil {
		a.logf("%s: %v", name, err)
		return
	}
	a.logf("%s set to %s.", name, opt.Name)
	// Other settings can change along with this one, so re-read them all.
	if status, vals, err := c.CameraState(context.Background()); err == nil {
		a.mu.Lock()
		a.setVals, a.setStatus = vals, status
		a.mu.Unlock()
	} else {
		a.mu.Lock()
		if a.setVals != nil {
			a.setVals[id] = opt.Value
		}
		a.mu.Unlock()
	}
	a.win.Invalidate()
}

// ---------- layout ----------

type (
	C = layout.Context
	D = layout.Dimensions
)

func fill(c color.NRGBA, radius unit.Dp) layout.Widget {
	return func(gtx C) D {
		sz := gtx.Constraints.Min
		r := gtx.Dp(radius)
		defer clip.UniformRRect(image.Rectangle{Max: sz}, r).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, c)
		return D{Size: sz}
	}
}

// panel draws w on a rounded surface with a hairline border, the card style used throughout.
func panel(gtx C, bg color.NRGBA, radius, inset unit.Dp, w layout.Widget) D {
	return widget.Border{Color: colLine, CornerRadius: radius, Width: unit.Dp(1)}.Layout(gtx, func(gtx C) D {
		return layout.Background{}.Layout(gtx, fill(bg, radius), func(gtx C) D {
			return layout.UniformInset(inset).Layout(gtx, w)
		})
	})
}

func card(gtx C, w layout.Widget) D {
	return panel(gtx, colPanel, 8, 16, w)
}

func hline(gtx C) D {
	sz := image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))
	defer clip.Rect{Max: sz}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, colLine)
	return D{Size: sz}
}

func vline(gtx C) D {
	sz := image.Pt(gtx.Dp(1), gtx.Constraints.Max.Y)
	defer clip.Rect{Max: sz}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, colLine)
	return D{Size: sz}
}

func (a *App) label(size unit.Sp, txt string, col color.NRGBA, bold bool) material.LabelStyle {
	l := material.Label(a.th, size, txt)
	l.Color = col
	if bold {
		l.Font.Weight = font.SemiBold
	}
	return l
}

func (a *App) button(btn *widget.Clickable, txt string, bg color.NRGBA, enabled bool) layout.Widget {
	return func(gtx C) D {
		b := material.Button(a.th, btn, txt)
		b.Background = bg
		b.Color = colBg
		b.CornerRadius = unit.Dp(6)
		b.TextSize = unit.Sp(13)
		b.Font.Weight = font.SemiBold
		b.Inset = layout.Inset{Top: unit.Dp(9), Bottom: unit.Dp(9), Left: unit.Dp(16), Right: unit.Dp(16)}
		if bg == colRed {
			b.Color = colText
		}
		if !enabled {
			b.Background = colRow
			b.Color = colMuted
			gtx = gtx.Disabled()
		}
		return b.Layout(gtx)
	}
}

// ghost is a quiet outlined secondary button.
func (a *App) ghost(btn *widget.Clickable, txt string, enabled bool) layout.Widget {
	return func(gtx C) D {
		b := material.Button(a.th, btn, txt)
		b.Background, b.Color = colPanel, colText
		b.CornerRadius = unit.Dp(6)
		b.TextSize = unit.Sp(13)
		b.Font.Weight = font.SemiBold
		b.Inset = layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(14), Right: unit.Dp(14)}
		if !enabled {
			b.Color = colMuted
			gtx = gtx.Disabled()
		}
		return widget.Border{Color: colLine, CornerRadius: unit.Dp(6), Width: unit.Dp(1)}.Layout(gtx, b.Layout)
	}
}

func (a *App) editor(ed *widget.Editor, hint string) layout.Widget {
	return func(gtx C) D {
		return widget.Border{Color: colLine, CornerRadius: unit.Dp(6), Width: unit.Dp(1)}.Layout(gtx, func(gtx C) D {
			return layout.Background{}.Layout(gtx, fill(colBg, 6), func(gtx C) D {
				return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(12), Right: unit.Dp(12)}.Layout(gtx, func(gtx C) D {
					e := material.Editor(a.th, ed, hint)
					e.TextSize = unit.Sp(13)
					e.HintColor = colMuted
					return e.Layout(gtx)
				})
			})
		})
	}
}

// chip is a small rounded tag, used for counts and media badges.
func (a *App) chip(txt string, bg, fg color.NRGBA) layout.Widget {
	return func(gtx C) D {
		return layout.Background{}.Layout(gtx, fill(bg, 4), func(gtx C) D {
			return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2), Left: unit.Dp(6), Right: unit.Dp(6)}.Layout(gtx,
				a.label(10, txt, fg, true).Layout)
		})
	}
}

func dot(col color.NRGBA, size unit.Dp) layout.Widget {
	return func(gtx C) D {
		s := gtx.Dp(size)
		defer clip.Ellipse(image.Rectangle{Max: image.Pt(s, s)}).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, col)
		return D{Size: image.Pt(s, s)}
	}
}

// meter is a thin rounded progress track.
func meter(frac float32, col color.NRGBA) layout.Widget {
	return func(gtx C) D {
		w, h := gtx.Constraints.Max.X, gtx.Dp(3)
		func() {
			defer clip.UniformRRect(image.Rect(0, 0, w, h), h/2).Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, colLine)
		}()
		frac = max(0, min(1, frac))
		if fw := int(float32(w) * frac); fw > 0 {
			defer clip.UniformRRect(image.Rect(0, 0, fw, h), h/2).Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, col)
		}
		return D{Size: image.Pt(w, h)}
	}
}

// checkbox draws the square selection box shown on media cards.
func checkbox(gtx C, checked bool) D {
	s := gtx.Dp(14)
	sz := image.Pt(s, s)
	r := gtx.Dp(3)
	if checked {
		func() {
			defer clip.UniformRRect(image.Rectangle{Max: sz}, r).Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, colAccent)
		}()
		var p clip.Path
		p.Begin(gtx.Ops)
		fs := float32(s)
		p.MoveTo(f32.Pt(fs*0.24, fs*0.52))
		p.LineTo(f32.Pt(fs*0.42, fs*0.70))
		p.LineTo(f32.Pt(fs*0.76, fs*0.32))
		paint.FillShape(gtx.Ops, colBg, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
		return D{Size: sz}
	}
	func() {
		defer clip.UniformRRect(image.Rectangle{Max: sz}, r).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, color.NRGBA{A: 0x80})
	}()
	gtx.Constraints = layout.Exact(sz)
	return widget.Border{Color: colText, CornerRadius: unit.Dp(3), Width: unit.Dp(1.2)}.Layout(gtx, func(gtx C) D {
		return D{Size: sz}
	})
}

func polygon(gtx C, col color.NRGBA, pts ...f32.Point) {
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(pts[0])
	for _, pt := range pts[1:] {
		p.LineTo(pt)
	}
	p.Close()
	paint.FillShape(gtx.Ops, col, clip.Outline{Path: p.End()}.Op())
}

func gradient(gtx C, r image.Rectangle, from, to f32.Point, c1, c2 color.NRGBA) {
	defer clip.Rect(r).Push(gtx.Ops).Pop()
	paint.LinearGradientOp{Stop1: from, Color1: c1, Stop2: to, Color2: c2}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// logoMark is the four-tile GoProBuddy mark.
func logoMark(gtx C) D {
	s, g := gtx.Dp(8), gtx.Dp(2)
	for i := 0; i < 4; i++ {
		x, y := (i%2)*(s+g), (i/2)*(s+g)
		col := colAccent
		if i == 1 {
			col = colText
		}
		func() {
			defer clip.UniformRRect(image.Rect(x, y, x+s, y+s), gtx.Dp(2)).Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, col)
		}()
	}
	return D{Size: image.Pt(2*s+g, 2*s+g)}
}

// cameraArt is the action-camera illustration on the sidebar camera card.
func cameraArt(gtx C, live bool) D {
	w, h := gtx.Dp(104), gtx.Dp(70)
	body := image.Rect(0, gtx.Dp(4), w, h)
	func() {
		defer clip.UniformRRect(body, gtx.Dp(12)).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, color.NRGBA{R: 0x2A, G: 0x30, B: 0x38, A: 0xFF})
	}()
	func() { // shutter button
		defer clip.UniformRRect(image.Rect(gtx.Dp(14), 0, gtx.Dp(34), gtx.Dp(8)), gtx.Dp(3)).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, colRow)
	}()
	scr := color.NRGBA{R: 0x0B, G: 0x0E, B: 0x12, A: 0xFF}
	if live {
		scr = color.NRGBA{R: 0x12, G: 0x3A, B: 0x4A, A: 0xFF}
	}
	func() { // front screen
		defer clip.UniformRRect(image.Rect(gtx.Dp(10), gtx.Dp(16), gtx.Dp(46), gtx.Dp(46)), gtx.Dp(4)).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, scr)
	}()
	ring := func(cx, cy, r int, col color.NRGBA) {
		defer clip.Ellipse(image.Rect(cx-r, cy-r, cx+r, cy+r)).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, col)
	}
	cx, cy := gtx.Dp(74), gtx.Dp(36)
	ring(cx, cy, gtx.Dp(23), color.NRGBA{R: 0x1A, G: 0x1E, B: 0x24, A: 0xFF})
	ring(cx, cy, gtx.Dp(17), color.NRGBA{R: 0x05, G: 0x07, B: 0x0A, A: 0xFF})
	ring(cx, cy, gtx.Dp(10), color.NRGBA{R: 0x10, G: 0x36, B: 0x4C, A: 0xFF})
	ring(cx-gtx.Dp(4), cy-gtx.Dp(4), gtx.Dp(3), color.NRGBA{R: 0x9F, G: 0xE6, B: 0xFF, A: 0xA0})
	return D{Size: image.Pt(w, h)}
}

// lens is the illustration on empty states: a camera lens in GoPro blue.
func lens(gtx C) D {
	s := gtx.Dp(64)
	ring := func(inset int, col color.NRGBA) {
		defer clip.Ellipse(image.Rect(inset, inset, s-inset, s-inset)).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, col)
	}
	ring(0, colRow)
	ring(gtx.Dp(12), colThumb)
	ring(gtx.Dp(20), colAccent)
	ring(gtx.Dp(27), colBg)
	return D{Size: image.Pt(s, s)}
}

func (a *App) emptyState(gtx C, title, body string, cta layout.Widget) D {
	return layout.Center.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(360))
		centered := func(l material.LabelStyle) layout.Widget {
			l.Alignment = text.Middle
			return func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return l.Layout(gtx)
			}
		}
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(lens),
			layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
			layout.Rigid(centered(a.label(17, title, colText, true))),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(centered(a.label(13, body, colMuted, false))),
			layout.Rigid(func(gtx C) D {
				if cta == nil {
					return D{}
				}
				return layout.Inset{Top: unit.Dp(18)}.Layout(gtx, cta)
			}),
		)
	})
}

func sectionTitle(a *App, txt string) layout.Widget {
	return a.label(10, txt, colMuted, true).Layout
}

var gap = layout.Spacer{Height: unit.Dp(12)}.Layout
var hgap = layout.Spacer{Width: unit.Dp(8)}.Layout

func (a *App) layout(gtx C) D {
	paint.Fill(gtx.Ops, colBg)
	return layout.Flex{}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			w := gtx.Dp(240)
			gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			return layout.Background{}.Layout(gtx, fill(colSidebar, 0), func(gtx C) D {
				return layout.UniformInset(unit.Dp(12)).Layout(gtx, a.sidebar)
			})
		}),
		layout.Rigid(vline),
		layout.Flexed(1, a.mainArea),
	)
}

func (a *App) sidebar(gtx C) D {
	a.mu.Lock()
	busy, connected, st := a.busy, a.connected, a.status
	nMedia, nSaved := len(a.media), len(a.saved)
	a.mu.Unlock()

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(16), Left: unit.Dp(6)}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(logoMark),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(a.label(17, "GoPro", colText, true).Layout),
					layout.Rigid(a.label(17, "Buddy", colAccent, true).Layout),
				)
			})
		}),
		layout.Rigid(func(gtx C) D { return a.cameraCard(gtx, busy, connected, st) }),
		layout.Rigid(layout.Spacer{Height: unit.Dp(22)}.Layout),
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Left: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, sectionTitle(a, "WORKSPACE"))
		}),
		layout.Rigid(a.navItem(pageMedia, "■", fmt.Sprint(nMedia))),
		layout.Rigid(a.navItem(pageDownloads, "↓", fmt.Sprint(nSaved))),
		layout.Rigid(a.navItem(pagePreview, "●", "")),
		layout.Rigid(a.navItem(pageWebcam, "◘", "")),
		layout.Rigid(a.navItem(pageSettings, "☼", "")),
		layout.Flexed(1, layout.Spacer{}.Layout),
		layout.Rigid(a.navItem(pageConnection, "↔", "")),
		layout.Rigid(a.navItem(pageActivity, "≡", "")),
		layout.Rigid(func(gtx C) D {
			txt, col := "GoProBuddy / Desktop", colMuted
			if diag.Enabled() {
				txt, col = "● Saving diagnostic log", colGreen
			}
			return layout.Inset{Top: unit.Dp(10), Left: unit.Dp(10)}.Layout(gtx, a.label(10, txt, col, false).Layout)
		}),
	)
}

func (a *App) navItem(page int, glyph, badge string) layout.Widget {
	return func(gtx C) D {
		btn := &a.navBtns[page]
		active := a.page == page
		return layout.Inset{Bottom: unit.Dp(2)}.Layout(gtx, func(gtx C) D {
			return btn.Layout(gtx, func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				bg, fg, badgeBg := color.NRGBA{}, colMuted, colRow
				switch {
				case active:
					bg, fg, badgeBg = color.NRGBA{R: 0x13, G: 0xBC, B: 0xF2, A: 0x1C}, colAccent, color.NRGBA{R: 0x13, G: 0xBC, B: 0xF2, A: 0x30}
				case btn.Hovered():
					bg, fg = colRow, colText
				}
				return layout.Background{}.Layout(gtx, fill(bg, 6), func(gtx C) D {
					return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(10), Right: unit.Dp(8)}.Layout(gtx, func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx C) D {
								gtx.Constraints.Min.X = gtx.Dp(24)
								return a.label(12, glyph, fg, false).Layout(gtx)
							}),
							layout.Flexed(1, a.label(13, pageNames[page], fg, active).Layout),
							layout.Rigid(func(gtx C) D {
								if badge == "" {
									return D{}
								}
								return a.chip(badge, badgeBg, fg)(gtx)
							}),
						)
					})
				})
			})
		})
	}
}

// cameraCard is the sidebar camera tile: who is connected, its vitals, and the shutter.
func (a *App) cameraCard(gtx C, busy string, connected bool, st gopro.Status) D {
	stateCol, state := colMuted, "No camera connected"
	title := "No camera"
	if connected {
		stateCol, state, title = colGreen, "Camera connected", "GoPro"
		if st.Model != "" {
			title = st.Model
		}
		if st.Recording {
			stateCol, state = colRed, "Recording"
		}
	}
	if busy != "" {
		stateCol, state = colAccent, busy
	}
	return panel(gtx, colPanel, 8, 12, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(dot(stateCol, 6)),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
					layout.Rigid(a.label(11, state, stateCol, false).Layout),
				)
			}),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(12)}.Layout(gtx, func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Center.Layout(gtx, func(gtx C) D { return cameraArt(gtx, connected) })
				})
			}),
			layout.Rigid(a.label(15, title, colText, true).Layout),
			layout.Rigid(func(gtx C) D {
				msg := "Your next adventure is in here."
				if !connected {
					msg = "Turn your GoPro on and keep it close."
				}
				return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, a.label(11, msg, colMuted, false).Layout)
			}),
			layout.Rigid(func(gtx C) D {
				if !connected {
					return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx C) D {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return a.button(&a.findCamBtn, "Find my camera", colAccent, busy == "")(gtx)
					})
				}
				batt, battCol, frac := "Battery —", colGreen, float32(0)
				if st.BatteryPercent >= 0 {
					batt, frac = fmt.Sprintf("%d%% battery", st.BatteryPercent), float32(st.BatteryPercent)/100
					if st.BatteryPercent <= 20 {
						battCol = colRed
					}
				}
				sd := ""
				switch {
				case st.NoSDCard:
					sd = "No SD card"
				case st.SDRemainingKB > 0:
					sd = humanSize(st.SDRemainingKB*1024) + " free"
				}
				return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							return layout.Flex{}.Layout(gtx,
								layout.Flexed(1, a.label(11, batt, battCol, false).Layout),
								layout.Rigid(a.label(11, sd, colMuted, false).Layout),
							)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
						layout.Rigid(meter(frac, battCol)),
						layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
						layout.Rigid(func(gtx C) D {
							txt := "●  Record"
							if st.Recording {
								txt = "■  Stop recording"
							}
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return a.button(&a.recordBtn, txt, colRed, true)(gtx)
						}),
					)
				})
			}),
		)
	})
}

func (a *App) mainArea(gtx C) D {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(a.topBar),
		layout.Rigid(hline),
		layout.Flexed(1, func(gtx C) D {
			switch a.page {
			case pageDownloads:
				return a.downloadsPage(gtx)
			case pagePreview:
				return a.pageBody(gtx, "See what your GoPro sees.", "A live, low-latency stream from the camera.", nil, func(gtx C) D {
					return a.streamView(gtx, "preview", &a.previewBtn, "Start preview")
				})
			case pageWebcam:
				return a.pageBody(gtx, "Use your GoPro as a webcam.", "Works over Wi-Fi or a USB-C cable. The cable gives the steadiest picture.", nil, a.webcamView)
			case pageSettings:
				a.mu.Lock()
				busy := a.settingsBusy
				a.mu.Unlock()
				label := "Reload"
				if busy {
					label = "Loading…"
				}
				return a.pageBody(gtx, "Camera settings", "Read from the camera, so you only see what your model supports.", a.ghost(&a.reloadSetBtn, label, !busy), a.settingsView)
			case pageConnection:
				return a.pageBody(gtx, "Camera connection", "Find your GoPro over Bluetooth, or connect over its Wi-Fi.", nil, a.connectionView)
			case pageActivity:
				label := "Start diagnostic log"
				if diag.Enabled() {
					label = "Stop diagnostic log"
				}
				return a.pageBody(gtx, "Activity", "What GoProBuddy has been up to.", func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(a.ghost(&a.diagFolderBtn, "Open log folder", true)),
						layout.Rigid(hgap),
						layout.Rigid(a.button(&a.diagBtn, label, colAccent, true)),
					)
				}, a.activityView)
			}
			return a.mediaPage(gtx)
		}),
	)
}

func (a *App) topBar(gtx C) D {
	a.mu.Lock()
	connected, model := a.connected, a.status.Model
	a.mu.Unlock()
	return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(12), Left: unit.Dp(22), Right: unit.Dp(22)}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(a.label(12, "Workspace", colMuted, false).Layout),
			layout.Rigid(a.label(12, "   /   ", colMuted, false).Layout),
			layout.Rigid(a.label(12, pageNames[a.page], colText, true).Layout),
			layout.Flexed(1, layout.Spacer{}.Layout),
			layout.Rigid(func(gtx C) D {
				col, txt := colMuted, "Not connected"
				if connected {
					col, txt = colGreen, "GoPro"
					if model != "" {
						txt = model
					}
				}
				return widget.Border{Color: colLine, CornerRadius: unit.Dp(10), Width: unit.Dp(1)}.Layout(gtx, func(gtx C) D {
					return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(10), Right: unit.Dp(10)}.Layout(gtx, func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(dot(col, 6)),
							layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
							layout.Rigid(a.label(11, txt, colText, false).Layout),
						)
					})
				})
			}),
		)
	})
}

func (a *App) pageHeader(title, sub string, action layout.Widget) layout.Widget {
	return func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(a.label(22, title, colText, true).Layout),
					layout.Rigid(layout.Spacer{Height: unit.Dp(2)}.Layout),
					layout.Rigid(a.label(12, sub, colMuted, false).Layout),
				)
			}),
			layout.Rigid(func(gtx C) D {
				if action == nil {
					return D{}
				}
				return action(gtx)
			}),
		)
	}
}

func (a *App) pageBody(gtx C, title, sub string, action, body layout.Widget) D {
	return layout.Inset{Top: unit.Dp(20), Bottom: unit.Dp(20), Left: unit.Dp(22), Right: unit.Dp(22)}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(a.pageHeader(title, sub, action)),
			layout.Rigid(layout.Spacer{Height: unit.Dp(18)}.Layout),
			layout.Flexed(1, body),
		)
	})
}

// hero is the landscape banner at the top of the media page.
func (a *App) hero(gtx C) D {
	w, h := gtx.Constraints.Max.X, gtx.Dp(150)
	sz := image.Pt(w, h)
	fw, fh := float32(w), float32(h)
	defer clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(8)).Push(gtx.Ops).Pop()
	gradient(gtx, image.Rectangle{Max: sz}, f32.Pt(0, 0), f32.Pt(0, fh),
		color.NRGBA{R: 0x2A, G: 0x4C, B: 0x5E, A: 0xFF}, color.NRGBA{R: 0x0E, G: 0x1C, B: 0x24, A: 0xFF})
	pt := func(x, y float32) f32.Point { return f32.Pt(fw*x, fh*y) }
	polygon(gtx, color.NRGBA{R: 0x3B, G: 0x5C, B: 0x6E, A: 0xFF},
		pt(0, .7), pt(.12, .48), pt(.22, .6), pt(.38, .28), pt(.5, .52), pt(.63, .18), pt(.75, .42), pt(.87, .14), pt(1, .36), pt(1, 1), pt(0, 1))
	polygon(gtx, color.NRGBA{R: 0xD8, G: 0xE6, B: 0xEE, A: 0x90}, // snow caps
		pt(.63, .18), pt(.6, .27), pt(.645, .25), pt(.67, .28))
	polygon(gtx, color.NRGBA{R: 0xD8, G: 0xE6, B: 0xEE, A: 0x90},
		pt(.87, .14), pt(.835, .24), pt(.875, .21), pt(.9, .23))
	polygon(gtx, color.NRGBA{R: 0x1B, G: 0x33, B: 0x3E, A: 0xFF},
		pt(0, .86), pt(.16, .66), pt(.3, .8), pt(.46, .58), pt(.6, .76), pt(.78, .52), pt(.9, .68), pt(1, .6), pt(1, 1), pt(0, 1))
	polygon(gtx, color.NRGBA{R: 0x0D, G: 0x1A, B: 0x1F, A: 0xFF},
		pt(0, .95), pt(.25, .84), pt(.5, .92), pt(.72, .82), pt(1, .9), pt(1, 1), pt(0, 1))
	gradient(gtx, image.Rectangle{Max: sz}, f32.Pt(0, 0), f32.Pt(fw*0.6, 0),
		color.NRGBA{R: 0x08, G: 0x0B, B: 0x0E, A: 0xD8}, color.NRGBA{R: 0x08, G: 0x0B, B: 0x0E, A: 0x00})

	gtx.Constraints = layout.Exact(sz)
	return layout.Stack{}.Layout(gtx,
		layout.Stacked(func(gtx C) D { return D{Size: sz} }),
		layout.Expanded(func(gtx C) D {
			return layout.W.Layout(gtx, func(gtx C) D {
				return layout.Inset{Left: unit.Dp(18)}.Layout(gtx, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(a.label(9, "LESS WAITING. MORE ADVENTURE.", colAccent, true).Layout),
						layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
						layout.Rigid(a.label(21, "Out there, you capture it.", colText, true).Layout),
						layout.Rigid(a.label(21, "Here, you keep it.", colText, true).Layout),
						layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
						layout.Rigid(a.label(11, "Original quality. Every moment. Straight to your desktop.", colMuted, false).Layout),
					)
				})
			})
		}),
		layout.Expanded(func(gtx C) D {
			return layout.SE.Layout(gtx, func(gtx C) D {
				return layout.Inset{Right: unit.Dp(14), Bottom: unit.Dp(10)}.Layout(gtx,
					a.label(9, "SHOT ON GOPRO   /   GO ANYWHERE.", color.NRGBA{R: 0xC8, G: 0xD2, B: 0xDC, A: 0xC0}, true).Layout)
			})
		}),
	)
}

type mediaGroup struct {
	day  time.Time
	rows []*mediaRow
	size int64
}

func dayLabel(t time.Time) string {
	now := time.Now()
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	switch {
	case !t.Before(today):
		return "Today, " + t.Format("January 2")
	case !t.Before(today.AddDate(0, 0, -1)):
		return "Yesterday, " + t.Format("January 2")
	case t.Year() == y:
		return t.Format("Monday, January 2")
	}
	return t.Format("Monday, January 2, 2006")
}

func (a *App) filterTabs(counts [filterCount]int) layout.Widget {
	names := [filterCount]string{"All media", "Videos", "Photos", "Not downloaded"}
	return func(gtx C) D {
		return panel(gtx, colPanel, 6, 3, func(gtx C) D {
			children := make([]layout.FlexChild, filterCount)
			for i := range names {
				children[i] = layout.Rigid(func(gtx C) D {
					btn := &a.filterBtns[i]
					active := a.filter == i
					return btn.Layout(gtx, func(gtx C) D {
						bg, fg := color.NRGBA{}, colMuted
						if active {
							bg, fg = colRow, colText
						} else if btn.Hovered() {
							fg = colText
						}
						return layout.Background{}.Layout(gtx, fill(bg, 4), func(gtx C) D {
							return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(10), Right: unit.Dp(10)}.Layout(gtx, func(gtx C) D {
								return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
									layout.Rigid(a.label(12, names[i], fg, active).Layout),
									layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
									layout.Rigid(a.label(10, fmt.Sprint(counts[i]), colMuted, false).Layout),
								)
							})
						})
					})
				})
			}
			return layout.Flex{}.Layout(gtx, children...)
		})
	}
}

func (a *App) mediaPage(gtx C) D {
	a.mu.Lock()
	rows, connected, busy, onDisk := a.media, a.connected, a.busy, a.onDisk
	noSD := a.status.NoSDCard
	a.mu.Unlock()

	var counts [filterCount]int
	q := strings.ToLower(strings.TrimSpace(a.searchEditor.Text()))
	var vis []*mediaRow
	for _, r := range rows {
		video, saved := r.item.IsVideo(), onDisk[r.item.Name]
		counts[filterAll]++
		if video {
			counts[filterVideos]++
		} else {
			counts[filterPhotos]++
		}
		if !saved {
			counts[filterNotSaved]++
		}
		switch {
		case a.filter == filterVideos && !video,
			a.filter == filterPhotos && video,
			a.filter == filterNotSaved && saved,
			q != "" && !strings.Contains(strings.ToLower(r.item.Name), q):
			continue
		}
		vis = append(vis, r)
	}
	a.visible = vis

	var groups []*mediaGroup
	for _, r := range vis {
		y, m, d := r.item.Created.Date()
		day := time.Date(y, m, d, 0, 0, 0, 0, r.item.Created.Location())
		if len(groups) == 0 || !groups[len(groups)-1].day.Equal(day) {
			groups = append(groups, &mediaGroup{day: day})
		}
		g := groups[len(groups)-1]
		g.rows = append(g.rows, r)
		g.size += r.item.Size
	}
	allSel := len(vis) > 0
	for _, r := range vis {
		allSel = allSel && r.selected
	}

	pad := func(w layout.Widget) layout.Widget {
		return func(gtx C) D {
			return layout.Inset{Left: unit.Dp(22), Right: unit.Dp(22)}.Layout(gtx, w)
		}
	}
	lines := []layout.Widget{
		pad(func(gtx C) D {
			return layout.Inset{Top: unit.Dp(20), Bottom: unit.Dp(16)}.Layout(gtx,
				a.pageHeader("Your adventures. All here.", "Everything on your GoPro, ready to come home.", a.ghost(&a.refreshBtn, "Refresh media", connected)))
		}),
		pad(a.hero),
		pad(func(gtx C) D {
			return layout.Inset{Top: unit.Dp(16), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(a.filterTabs(counts)),
					layout.Flexed(1, layout.Spacer{}.Layout),
					layout.Rigid(func(gtx C) D {
						w := gtx.Dp(200)
						gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
						return a.editor(&a.searchEditor, "Search files…")(gtx)
					}),
				)
			})
		}),
	}

	if len(vis) == 0 {
		title, body, cta := "No matches", "Try another filter or search.", layout.Widget(nil)
		switch {
		case len(rows) == 0 && !connected:
			title, body = "Connect to your GoPro", "Make sure your camera is on and nearby. Your photos and videos will show up here."
			cta = a.button(&a.emptyScanBtn, "Find my camera", colAccent, busy == "")
		case len(rows) == 0 && noSD:
			title, body = "No SD card", "Insert a microSD card into the camera, then press Refresh."
		case len(rows) == 0:
			title, body = "No media yet", "Shoot something, then press Refresh."
		case a.filter == filterNotSaved && q == "":
			title, body = "All caught up", "Everything on the camera is already on your computer."
		}
		lines = append(lines, pad(func(gtx C) D {
			gtx.Constraints.Min.Y = gtx.Dp(300)
			return a.emptyState(gtx, title, body, cta)
		}))
	}

	cols := max(2, (gtx.Constraints.Max.X-gtx.Dp(44))/gtx.Dp(220))
	for gi, g := range groups {
		first := gi == 0
		lines = append(lines, pad(func(gtx C) D {
			return layout.Inset{Top: unit.Dp(18), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(a.label(13, dayLabel(g.day), colText, true).Layout),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(a.label(11, fmt.Sprintf("%d files · %s", len(g.rows), humanSize(g.size)), colMuted, false).Layout),
					layout.Flexed(1, layout.Spacer{}.Layout),
					layout.Rigid(func(gtx C) D {
						if !first {
							return D{}
						}
						return a.selectAllBtn.Layout(gtx, func(gtx C) D {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx C) D { return checkbox(gtx, allSel) }),
								layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
								layout.Rigid(a.label(11, "Select all", colMuted, false).Layout),
							)
						})
					}),
				)
			})
		}))
		for start := 0; start < len(g.rows); start += cols {
			line := g.rows[start:min(start+cols, len(g.rows))]
			lines = append(lines, func(gtx C) D {
				return layout.Inset{Left: unit.Dp(17), Right: unit.Dp(17)}.Layout(gtx, func(gtx C) D {
					children := make([]layout.FlexChild, cols)
					for c := range children {
						children[c] = layout.Flexed(1, func(gtx C) D {
							if c >= len(line) {
								return D{Size: image.Pt(gtx.Constraints.Max.X, 0)}
							}
							return layout.UniformInset(unit.Dp(5)).Layout(gtx, func(gtx C) D {
								return a.mediaTile(gtx, line[c], onDisk[line[c].item.Name])
							})
						})
					}
					return layout.Flex{}.Layout(gtx, children...)
				})
			})
		}
	}
	lines = append(lines, layout.Spacer{Height: unit.Dp(20)}.Layout)

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D {
			gtx.Constraints.Min = gtx.Constraints.Max
			return material.List(a.th, &a.mediaList).Layout(gtx, len(lines), func(gtx C, i int) D {
				return lines[i](gtx)
			})
		}),
		layout.Rigid(hline),
		layout.Rigid(a.footer),
	)
}

func (a *App) mediaTile(gtx C, r *mediaRow, saved bool) D {
	a.requestThumb(r.item)
	a.mu.Lock()
	thumb, hasThumb := a.thumbs[r.item.Path()]
	a.mu.Unlock()

	return r.tile.Layout(gtx, func(gtx C) D {
		bc, bw := colLine, unit.Dp(1)
		switch {
		case r.selected:
			bc, bw = colAccent, unit.Dp(2)
		case r.tile.Hovered():
			bc = color.NRGBA{R: 0x3A, G: 0x43, B: 0x4E, A: 0xFF}
		}
		return widget.Border{Color: bc, CornerRadius: unit.Dp(6), Width: bw}.Layout(gtx, func(gtx C) D {
			return layout.Background{}.Layout(gtx, fill(colPanel, 6), func(gtx C) D {
				w := gtx.Constraints.Max.X
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						sz := image.Pt(w, w*9/16)
						gtx.Constraints = layout.Exact(sz)
						rr := gtx.Dp(6)
						defer clip.RRect{Rect: image.Rectangle{Max: sz}, NW: rr, NE: rr}.Push(gtx.Ops).Pop()
						return layout.Stack{}.Layout(gtx,
							layout.Stacked(func(gtx C) D {
								if !hasThumb {
									return fill(colThumb, 0)(gtx)
								}
								return widget.Image{Src: thumb, Fit: widget.Cover, Position: layout.Center}.Layout(gtx)
							}),
							layout.Expanded(func(gtx C) D {
								switch {
								case r.selected:
									paint.Fill(gtx.Ops, color.NRGBA{R: 0x13, G: 0xBC, B: 0xF2, A: 0x30})
								case r.tile.Hovered():
									paint.Fill(gtx.Ops, color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0x10})
								}
								return D{Size: gtx.Constraints.Min}
							}),
							layout.Expanded(func(gtx C) D {
								return layout.NW.Layout(gtx, func(gtx C) D {
									return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx C) D { return checkbox(gtx, r.selected) })
								})
							}),
							layout.Expanded(func(gtx C) D {
								kind := "PHOTO"
								if r.item.IsVideo() {
									kind = "VIDEO"
								}
								return layout.NE.Layout(gtx, func(gtx C) D {
									return layout.UniformInset(unit.Dp(8)).Layout(gtx, a.chip(kind, color.NRGBA{A: 0xB0}, colText))
								})
							}),
							layout.Expanded(func(gtx C) D {
								if !r.item.IsVideo() {
									return D{}
								}
								return layout.Center.Layout(gtx, func(gtx C) D {
									s := gtx.Dp(26)
									defer clip.Ellipse(image.Rectangle{Max: image.Pt(s, s)}).Push(gtx.Ops).Pop()
									paint.Fill(gtx.Ops, color.NRGBA{A: 0x70})
									gtx.Constraints = layout.Exact(image.Pt(s, s))
									return layout.Center.Layout(gtx, a.label(10, "▶", colText, false).Layout)
								})
							}),
						)
					}),
					layout.Rigid(func(gtx C) D {
						return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(9), Left: unit.Dp(9), Right: unit.Dp(9)}.Layout(gtx, func(gtx C) D {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(func(gtx C) D {
									return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
										layout.Flexed(1, func(gtx C) D {
											l := a.label(12, r.item.Name, colText, true)
											l.MaxLines = 1
											return l.Layout(gtx)
										}),
										layout.Rigid(func(gtx C) D {
											if !saved {
												return D{}
											}
											return a.label(10, "● Saved", colGreen, true).Layout(gtx)
										}),
									)
								}),
								layout.Rigid(layout.Spacer{Height: unit.Dp(3)}.Layout),
								layout.Rigid(func(gtx C) D {
									kind := "Photo"
									if r.item.IsVideo() {
										kind = "Video"
									}
									meta := kind
									if r.item.Size > 0 {
										meta = humanSize(r.item.Size) + " · " + kind
									}
									return layout.Flex{}.Layout(gtx,
										layout.Flexed(1, a.label(10, meta, colMuted, false).Layout),
										layout.Rigid(a.label(10, r.item.Created.Format("15:04"), colMuted, false).Layout),
									)
								}),
							)
						})
					}),
				)
			})
		})
	})
}

// footer is the save bar under the media grid: destination folder and the download action,
// or progress while downloading.
func (a *App) footer(gtx C) D {
	a.mu.Lock()
	rows, onDisk, connected := a.media, a.onDisk, a.connected
	dlActive, dlName, dlDone, dlTotal, dlIndex, dlCount := a.dlActive, a.dlName, a.dlDone, a.dlTotal, a.dlIndex, a.dlCount
	a.mu.Unlock()

	nSel, ready, readySize := 0, 0, int64(0)
	for _, r := range rows {
		if r.selected {
			nSel++
		}
	}
	for _, r := range rows {
		if (nSel > 0 && r.selected) || (nSel == 0 && !onDisk[r.item.Name]) {
			ready++
			readySize += r.item.Size
		}
	}

	return layout.Background{}.Layout(gtx, fill(colSidebar, 0), func(gtx C) D {
		return layout.Inset{Top: unit.Dp(10), Bottom: unit.Dp(10), Left: unit.Dp(22), Right: unit.Dp(22)}.Layout(gtx, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return a.openFolderBtn.Layout(gtx, func(gtx C) D {
						s := gtx.Dp(32)
						gtx.Constraints = layout.Exact(image.Pt(s, s))
						return widget.Border{Color: colLine, CornerRadius: unit.Dp(6), Width: unit.Dp(1)}.Layout(gtx, func(gtx C) D {
							return layout.Center.Layout(gtx, a.label(13, "⌂", colMuted, false).Layout)
						})
					})
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(a.label(9, "SAVE TO", colMuted, true).Layout),
						layout.Rigid(func(gtx C) D {
							e := material.Editor(a.th, &a.folderEditor, "Choose a folder")
							e.TextSize = unit.Sp(12)
							e.HintColor = colMuted
							return e.Layout(gtx)
						}),
					)
				}),
				layout.Rigid(func(gtx C) D {
					if dlActive {
						var p float32
						if dlTotal > 0 {
							p = float32(dlDone) / float32(dlTotal)
						}
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx C) D {
								w := gtx.Dp(260)
								gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
								return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
									layout.Rigid(func(gtx C) D {
										l := a.label(12, fmt.Sprintf("Saving %d of %d · %s", dlIndex, dlCount, dlName), colText, true)
										l.MaxLines = 1
										return l.Layout(gtx)
									}),
									layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
									layout.Rigid(meter(p, colAccent)),
									layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
									layout.Rigid(a.label(10, humanSize(dlDone)+" / "+humanSize(dlTotal), colMuted, false).Layout),
								)
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(14)}.Layout),
							layout.Rigid(a.ghost(&a.cancelBtn, "Cancel", true)),
						)
					}
					label := "↓  Download all"
					if nSel > 0 {
						label = fmt.Sprintf("↓  Download %d selected", nSel)
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							return layout.Flex{Axis: layout.Vertical, Alignment: layout.End}.Layout(gtx,
								layout.Rigid(a.label(12, fmt.Sprintf("%d files ready", ready), colText, true).Layout),
								layout.Rigid(a.label(10, humanSize(readySize)+" · Original quality", colMuted, false).Layout),
							)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(14)}.Layout),
						layout.Rigid(func(gtx C) D {
							if nSel == 0 {
								return D{}
							}
							return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, a.ghost(&a.clearBtn, "Clear", true))
						}),
						layout.Rigid(a.button(&a.downloadBtn, label, colAccent, connected && ready > 0)),
					)
				}),
			)
		})
	})
}

func (a *App) downloadsPage(gtx C) D {
	a.mu.Lock()
	saved := append([]string(nil), a.saved...)
	dlActive, dlName, dlDone, dlTotal, dlIndex, dlCount := a.dlActive, a.dlName, a.dlDone, a.dlTotal, a.dlIndex, a.dlCount
	a.mu.Unlock()

	return a.pageBody(gtx, "Downloads", "Files saved to your computer this session.", a.ghost(&a.openFolderBtn, "Open folder", true), func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				if !dlActive {
					return D{}
				}
				var p float32
				if dlTotal > 0 {
					p = float32(dlDone) / float32(dlTotal)
				}
				return layout.Inset{Bottom: unit.Dp(14)}.Layout(gtx, func(gtx C) D {
					return card(gtx, func(gtx C) D {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx C) D {
								return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
									layout.Flexed(1, a.label(13, fmt.Sprintf("Saving %d of %d · %s", dlIndex, dlCount, dlName), colText, true).Layout),
									layout.Rigid(a.label(11, humanSize(dlDone)+" / "+humanSize(dlTotal), colMuted, false).Layout),
									layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
									layout.Rigid(a.ghost(&a.cancelBtn, "Cancel", true)),
								)
							}),
							layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
							layout.Rigid(meter(p, colAccent)),
						)
					})
				})
			}),
			layout.Flexed(1, func(gtx C) D {
				if len(saved) == 0 {
					if dlActive {
						return D{}
					}
					return a.emptyState(gtx, "Nothing saved yet", "Pick some clips on Camera media and press Download.", nil)
				}
				return material.List(a.th, &a.dlList).Layout(gtx, len(saved), func(gtx C, i int) D {
					name := saved[len(saved)-1-i]
					return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx C) D {
						return panel(gtx, colPanel, 6, 12, func(gtx C) D {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Flexed(1, a.label(13, name, colText, true).Layout),
								layout.Rigid(a.label(11, "● Saved", colGreen, true).Layout),
							)
						})
					})
				})
			}),
		)
	})
}

func (a *App) connectionView(gtx C) D {
	a.mu.Lock()
	busy, found := a.busy, a.found
	a.mu.Unlock()

	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(560))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return card(gtx, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, sectionTitle(a, "NEARBY CAMERAS · BLUETOOTH")),
							layout.Rigid(func(gtx C) D {
								t := "Scan"
								if busy == "Scanning…" {
									t = "Scanning…"
								}
								return a.ghost(&a.scanBtn, t, busy == "")(gtx)
							}),
						)
					}),
					layout.Rigid(func(gtx C) D {
						if len(found) == 0 {
							return layout.Inset{Top: unit.Dp(8)}.Layout(gtx,
								a.label(13, "Turn your GoPro on and keep it close. We'll switch on its Wi-Fi and join it for you.", colMuted, false).Layout)
						}
						gtx.Constraints.Max.Y = gtx.Dp(240)
						return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx C) D {
							return material.List(a.th, &a.foundList).Layout(gtx, len(found), func(gtx C, i int) D {
								f := found[i]
								return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx C) D {
									gtx.Constraints.Min.X = gtx.Constraints.Max.X
									return panel(gtx, colRow, 6, 8, func(gtx C) D {
										return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
											layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
											layout.Flexed(1, func(gtx C) D {
												return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
													layout.Rigid(a.label(13, f.cam.Name, colText, true).Layout),
													layout.Rigid(a.label(11, fmt.Sprintf("Signal %d dBm", f.cam.RSSI), colMuted, false).Layout),
												)
											}),
											layout.Rigid(a.button(&f.connect, "Connect", colAccent, busy == "")),
										)
									})
								})
							})
						})
					}),
				)
			})
		}),
		layout.Rigid(gap),
		layout.Rigid(func(gtx C) D {
			return card(gtx, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, sectionTitle(a, "USB-C CABLE · BEST FOR WEBCAM")),
							layout.Rigid(a.ghost(&a.usbBtn, "Connect over USB", busy == "")),
						)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
					layout.Rigid(a.label(13, "Set the camera's USB connection to GoPro Connect (Preferences → Connections), then plug it in.", colMuted, false).Layout),
				)
			})
		}),
		layout.Rigid(gap),
		layout.Rigid(func(gtx C) D {
			return card(gtx, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(sectionTitle(a, "ALREADY ON CAMERA WI-FI?")),
					layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
					layout.Rigid(func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, a.editor(&a.ipEditor, "Camera IP")),
							layout.Rigid(hgap),
							layout.Rigid(a.button(&a.wifiBtn, "Connect", colAccent, busy == "")),
						)
					}),
				)
			})
		}),
	)
}

func (a *App) activityView(gtx C) D {
	a.mu.Lock()
	logs := append([]string(nil), a.logs...)
	a.mu.Unlock()
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			on := diag.Enabled()
			dotCol, title, body := colMuted, "Diagnostic log is off", "Having trouble? Start the diagnostic log, repeat what went wrong, then send us the log file. Wi-Fi passwords are never written to it."
			if on {
				dotCol, title, body = colGreen, "Diagnostic log is on", "Everything GoProBuddy does is being written to "+diag.Path()+". It stays on after a restart until you stop it."
			}
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx C) D {
				return card(gtx, func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(dot(dotCol, 8)),
								layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
								layout.Rigid(a.label(14, title, colText, true).Layout),
							)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
						layout.Rigid(a.label(12, body, colMuted, false).Layout),
					)
				})
			})
		}),
		layout.Flexed(1, func(gtx C) D { return a.logList(gtx, logs) }),
	)
}

func (a *App) logList(gtx C, logs []string) D {
	gtx.Constraints.Min = gtx.Constraints.Max
	return panel(gtx, colPanel, 8, 14, func(gtx C) D {
		gtx.Constraints.Min = gtx.Constraints.Max
		return material.List(a.th, &a.logLst).Layout(gtx, len(logs), func(gtx C, i int) D {
			col := colMuted
			if i == len(logs)-1 {
				col = colText
			}
			return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, a.label(12, logs[i], col, false).Layout)
		})
	})
}

func (a *App) streamView(gtx C, kind string, btn *widget.Clickable, startLabel string) D {
	a.mu.Lock()
	on, connected := a.previewOn && a.streamKind == kind, a.connected
	other, otherKind, port := a.previewOn && a.streamKind != kind, a.streamKind, a.streamPort
	if a.frameNew && a.frame != nil {
		a.frameOp = paint.NewImageOp(a.frame)
		a.frameNew = false
	}
	frameOp, hasFrame := a.frameOp, a.frame != nil && on
	a.mu.Unlock()

	gtx.Constraints.Min = gtx.Constraints.Max
	defer clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Max}, gtx.Dp(8)).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{A: 0xFF})
	return layout.Stack{}.Layout(gtx,
		layout.Stacked(func(gtx C) D {
			gtx.Constraints.Min = gtx.Constraints.Max
			if hasFrame {
				return widget.Image{Src: frameOp, Fit: widget.Contain, Position: layout.Center}.Layout(gtx)
			}
			if !connected && !on {
				return a.emptyState(gtx, "No camera connected", "Connect your GoPro to see what it sees, live.", nil)
			}
			msg, body := "Stream is off", "Low-latency stream from the camera, decoded with FFmpeg."
			switch {
			case other:
				msg, body = "Camera is busy", "The "+otherKind+" is running. Stop it first."
			case on && kind == "webcam" && a.webcamExternal:
				msg, body = "Streaming to other apps", fmt.Sprintf("Open udp://@:%d in OBS (Media Source, untick Local File) or VLC.", port)
			case on:
				msg = "Waiting for video…"
			}
			return a.emptyState(gtx, msg, body, nil)
		}),
		layout.Expanded(func(gtx C) D {
			if !on {
				return D{}
			}
			return layout.NW.Layout(gtx, func(gtx C) D {
				return layout.UniformInset(unit.Dp(14)).Layout(gtx, a.chip("● LIVE", colRed, colText))
			})
		}),
		layout.Expanded(func(gtx C) D {
			return layout.S.Layout(gtx, func(gtx C) D {
				return layout.Inset{Bottom: unit.Dp(18)}.Layout(gtx, func(gtx C) D {
					gtx.Constraints.Min = image.Point{}
					if on {
						return a.ghost(btn, "Stop", true)(gtx)
					}
					return a.button(btn, startLabel, colAccent, connected && !other)(gtx)
				})
			})
		}),
	)
}

func humanSize(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.2f GB", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.0f KB", float64(n)/k)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// wrap lays children out left to right, starting a new line when one does not fit.
func wrap(gtx C, spacing unit.Dp, children []layout.Widget) D {
	sp := gtx.Dp(spacing)
	maxW := gtx.Constraints.Max.X
	x, y, lineH, width := 0, 0, 0, 0
	for _, w := range children {
		m := op.Record(gtx.Ops)
		cgtx := gtx
		cgtx.Constraints.Min = image.Point{}
		d := w(cgtx)
		call := m.Stop()
		if x > 0 && x+d.Size.X > maxW {
			x, y, lineH = 0, y+lineH+sp, 0
		}
		t := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		x += d.Size.X + sp
		lineH = max(lineH, d.Size.Y)
		width = max(width, x-sp)
	}
	return D{Size: image.Pt(width, y+lineH)}
}

// pill is a selectable option button.
func (a *App) pill(btn *widget.Clickable, txt string, active bool) layout.Widget {
	return func(gtx C) D {
		return btn.Layout(gtx, func(gtx C) D {
			bg, fg, bc := colPanel, colMuted, colLine
			switch {
			case active:
				bg, fg, bc = color.NRGBA{R: 0x13, G: 0xBC, B: 0xF2, A: 0x22}, colAccent, colAccent
			case btn.Hovered():
				fg = colText
			}
			return widget.Border{Color: bc, CornerRadius: unit.Dp(6), Width: unit.Dp(1)}.Layout(gtx, func(gtx C) D {
				return layout.Background{}.Layout(gtx, fill(bg, 6), func(gtx C) D {
					return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(12), Right: unit.Dp(12)}.Layout(gtx,
						a.label(12, txt, fg, active).Layout)
				})
			})
		})
	}
}

func (a *App) settingsView(gtx C) D {
	a.mu.Lock()
	rows, vals, busy, errMsg, connected := a.settings, a.setVals, a.settingsBusy, a.setErr, a.connected
	blocked := gopro.Blocked(a.setFilters, a.setStatus, a.setVals)
	a.mu.Unlock()

	switch {
	case !connected && rows == nil:
		return a.emptyState(gtx, "No camera connected", "Connect your GoPro to change its settings.", a.button(&a.findCamBtn, "Find my camera", colAccent, true))
	case busy && rows == nil:
		return a.emptyState(gtx, "Reading settings…", "Asking the camera what it supports.", nil)
	case errMsg != "" && rows == nil:
		return a.emptyState(gtx, "Couldn't read settings", errMsg, nil)
	case rows == nil:
		return a.emptyState(gtx, "Settings not loaded", "Press Reload to read them from the camera.", nil)
	}

	// One line per group title, then one per setting, so the list scrolls smoothly.
	var lines []layout.Widget
	group := ""
	for _, r := range rows {
		allowed := 0
		for _, o := range r.Options {
			if !blocked[r.ID][o.Value] {
				allowed++
			}
		}
		if allowed == 0 {
			continue // does not apply in the camera's current mode
		}
		if r.Group != group {
			group = r.Group
			title := strings.ToUpper(group)
			top := unit.Dp(18)
			if len(lines) == 0 {
				top = 0
			}
			lines = append(lines, func(gtx C) D {
				return layout.Inset{Top: top, Bottom: unit.Dp(8), Left: unit.Dp(2)}.Layout(gtx, sectionTitle(a, title))
			})
		}
		lines = append(lines, func(gtx C) D { return a.settingRowView(gtx, r, vals, blocked[r.ID]) })
	}
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(760))
	return material.List(a.th, &a.settingsList).Layout(gtx, len(lines), func(gtx C, i int) D {
		return lines[i](gtx)
	})
}

func (a *App) settingRowView(gtx C, r *settingRow, vals map[int]int, blocked map[int]bool) D {
	cur, hasCur := vals[r.ID]
	curName := "—"
	for _, o := range r.Options {
		if hasCur && o.Value == cur {
			curName = o.Name
		}
	}
	open := a.openSetting == r.ID
	return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx C) D {
		return panel(gtx, colPanel, 6, 0, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return r.open.Layout(gtx, func(gtx C) D {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.Inset{Top: unit.Dp(11), Bottom: unit.Dp(11), Left: unit.Dp(14), Right: unit.Dp(14)}.Layout(gtx, func(gtx C) D {
							arrow := "▼"
							if open {
								arrow = "▲"
							}
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Flexed(1, a.label(13, r.Name, colText, true).Layout),
								layout.Rigid(a.label(13, curName, colAccent, false).Layout),
								layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
								layout.Rigid(a.label(9, arrow, colMuted, false).Layout),
							)
						})
					})
				}),
				layout.Rigid(func(gtx C) D {
					if !open {
						return D{}
					}
					var pills []layout.Widget
					for i, o := range r.Options {
						if blocked[o.Value] {
							continue
						}
						pills = append(pills, a.pill(&r.options[i], o.Name, hasCur && o.Value == cur))
					}
					return layout.Inset{Left: unit.Dp(14), Right: unit.Dp(14), Bottom: unit.Dp(12)}.Layout(gtx, func(gtx C) D {
						return wrap(gtx, 6, pills)
					})
				}),
			)
		})
	})
}

// watchUSB keeps usbIP current so the Webcam page can say whether a cable link exists.
func (a *App) watchUSB() {
	for {
		ip := gopro.USBCameraAddress()
		a.mu.Lock()
		changed := ip != a.usbIP
		a.usbIP = ip
		a.mu.Unlock()
		if changed {
			diag.Logf("usb", "USB-C camera link: %q", ip)
			a.win.Invalidate()
		}
		time.Sleep(3 * time.Second)
	}
}

func (a *App) webcamView(gtx C) D {
	a.mu.Lock()
	running := a.previewOn && a.streamKind == "webcam"
	usbIP, connected := a.usbIP, a.connected
	a.mu.Unlock()

	choice := func(title string, opts []gopro.SettingOption, btns []widget.Clickable, sel int) layout.Widget {
		return func(gtx C) D {
			pills := make([]layout.Widget, len(opts))
			for i, o := range opts {
				pills[i] = a.pill(&btns[i], o.Name, i == sel)
			}
			if running {
				gtx = gtx.Disabled()
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(sectionTitle(a, title)),
				layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx C) D { return wrap(gtx, 6, pills) }),
			)
		}
	}
	mode := 0
	if a.webcamExternal {
		mode = 1
	}
	outputs := []gopro.SettingOption{{Value: 0, Name: "GoProBuddy"}, {Value: 1, Name: "Other apps (OBS, VLC)"}}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			col, title, body := colMuted, "Using Wi-Fi", "Webcam mode works over the camera's Wi-Fi. For a steadier picture, plug the camera in with USB-C (Preferences → Connections → USB Connection: GoPro Connect) and GoProBuddy will switch to the cable automatically."
			if usbIP != "" {
				col, title, body = colGreen, "Camera connected by USB-C", "Webcam mode will use the cable ("+usbIP+"), even if you connected over Wi-Fi."
			} else if !connected {
				body = "Connect your GoPro first. " + body
			}
			return layout.Inset{Bottom: unit.Dp(14)}.Layout(gtx, func(gtx C) D {
				return panel(gtx, colPanel, 8, 12, func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Alignment: layout.Start}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							return layout.Inset{Top: unit.Dp(5), Right: unit.Dp(10)}.Layout(gtx, dot(col, 8))
						}),
						layout.Flexed(1, func(gtx C) D {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(a.label(13, title, colText, true).Layout),
								layout.Rigid(layout.Spacer{Height: unit.Dp(3)}.Layout),
								layout.Rigid(a.label(12, body, colMuted, false).Layout),
							)
						}),
					)
				})
			})
		}),
		layout.Rigid(func(gtx C) D {
			return layout.Flex{}.Layout(gtx,
				layout.Rigid(choice("RESOLUTION", gopro.WebcamResolutions, a.resBtns, a.webcamRes)),
				layout.Rigid(layout.Spacer{Width: unit.Dp(28)}.Layout),
				layout.Rigid(choice("LENS", gopro.WebcamFOVs, a.fovBtns, a.webcamFOV)),
				layout.Rigid(layout.Spacer{Width: unit.Dp(28)}.Layout),
				layout.Rigid(choice("SHOW VIDEO IN", outputs, a.webcamModeBtns[:], mode)),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
		layout.Flexed(1, func(gtx C) D {
			return a.streamView(gtx, "webcam", &a.webcamBtn, "Start webcam")
		}),
	)
}

// logState writes a snapshot of the app's state to the diagnostic log, so a log started
// mid-session still says what was going on.
func (a *App) logState() {
	a.mu.Lock()
	defer a.mu.Unlock()
	ip := ""
	if a.client != nil {
		ip = a.client.IP
	}
	_, ffErr := gopro.FindFFmpeg()
	diag.Logf("state", "connected=%v ip=%q ble=%v busy=%q status=%+v", a.connected, ip, a.ble != nil, a.busy, a.status)
	diag.Logf("state", "media=%d onDisk=%d saved=%d downloading=%v stream=%v kind=%q folder=%q ffmpegFound=%v",
		len(a.media), len(a.onDisk), len(a.saved), a.dlActive, a.previewOn, a.streamKind, a.lastFolder, ffErr == nil)
}
