// Package diag writes an optional diagnostic log file that users can attach to bug reports.
// Logging is off by default; when on, every Logf call is appended to a timestamped file in
// Dir(). Nothing secret (Wi-Fi passwords) should be passed to Logf; use Redact.
package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// keepFiles is how many old log files are kept; older ones are deleted when a new one starts.
const keepFiles = 10

var (
	mu   sync.Mutex
	file *os.File
	path string
)

// BaseDir is the app's data folder, e.g. %LOCALAPPDATA%\GoProBuddy.
func BaseDir() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "GoProBuddy")
}

// Dir is where log files are written.
func Dir() string { return filepath.Join(BaseDir(), "logs") }

func prefFile() string { return filepath.Join(BaseDir(), "diagnostic-logging.on") }

// WantEnabled reports whether the user left logging switched on last time.
func WantEnabled() bool {
	_, err := os.Stat(prefFile())
	return err == nil
}

// Enable starts a new log file and remembers the choice for the next launch.
// It returns the file's path.
func Enable(appInfo string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		return path, nil
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return "", err
	}
	prune()
	p := filepath.Join(Dir(), "goprobuddy-"+time.Now().Format("20060102-150405")+".log")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	file, path = f, p
	_ = os.WriteFile(prefFile(), nil, 0o644)
	host, _ := os.Hostname()
	fmt.Fprintf(file, "==== GoProBuddy diagnostic log ====\n%s\nstarted: %s\nos: %s/%s  go: %s  cpus: %d  host: %s\n====\n",
		appInfo, time.Now().Format(time.RFC1123Z), runtime.GOOS, runtime.GOARCH, runtime.Version(), runtime.NumCPU(), host)
	return path, nil
}

// Disable closes the log file and remembers the choice.
func Disable() {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		fmt.Fprintf(file, "%s [diag] logging switched off\n", stamp())
		_ = file.Close()
		file = nil
	}
	_ = os.Remove(prefFile())
}

// Enabled reports whether a log file is open.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return file != nil
}

// Path returns the current (or last) log file path.
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}

// Logf appends one line tagged with area (e.g. "http", "ble") when logging is on.
func Logf(area, format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return
	}
	msg := strings.TrimRight(fmt.Sprintf(format, args...), "\n")
	fmt.Fprintf(file, "%s [%s] %s\n", stamp(), area, msg)
}

// Writer adapts diag to io.Writer, for the standard log package and helper process output.
type Writer string

func (w Writer) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\r\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			Logf(string(w), "%s", line)
		}
	}
	return len(p), nil
}

// Redact hides a secret but keeps its length, which is often enough to debug with.
func Redact(s string) string { return fmt.Sprintf("<redacted, %d chars>", len(s)) }

func stamp() string { return time.Now().Format("2006-01-02 15:04:05.000") }

// prune deletes all but the newest keepFiles-1 logs (the new one makes keepFiles).
func prune() {
	m, _ := filepath.Glob(filepath.Join(Dir(), "goprobuddy-*.log"))
	sort.Strings(m) // names sort by time
	for len(m) > keepFiles-1 {
		_ = os.Remove(m[0])
		m = m[1:]
	}
}
