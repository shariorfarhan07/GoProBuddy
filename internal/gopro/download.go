package gopro

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"goprobuddy/internal/diag"
)

// Download saves a media file into dir. It resumes a previous partial download
// (<name>.part) and skips files that already exist with the right size.
// progress receives bytes done and total (total may be 0 if unknown).
func (c *Client) Download(ctx context.Context, m MediaItem, dir string, progress func(done, total int64)) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		diag.Logf("download", "cannot create folder %s: %v", dir, err)
		return "", err
	}
	final := filepath.Join(dir, m.Name)
	if fi, err := os.Stat(final); err == nil && (m.Size == 0 || fi.Size() == m.Size) {
		diag.Logf("download", "%s already saved (%d bytes), skipping", m.Path(), fi.Size())
		progress(fi.Size(), fi.Size())
		return final, nil
	}
	part := final + ".part"
	var offset int64
	if fi, err := os.Stat(part); err == nil {
		offset = fi.Size()
	}

	start := time.Now()
	diag.Logf("download", "start %s (camera size %d bytes, resuming from %d) from %s", m.Path(), m.Size, offset, c.FileURL(m))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.FileURL(m), nil)
	if err != nil {
		return "", err
	}
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	// Large files: no overall timeout, the context cancels.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		diag.Logf("download", "%s: request failed: %v", m.Name, err)
		return "", err
	}
	defer resp.Body.Close()

	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		if offset > 0 {
			diag.Logf("download", "%s: camera ignored the resume request, starting over", m.Name)
		}
		offset = 0 // server ignored Range; start over
		flags |= os.O_TRUNC
	default:
		diag.Logf("download", "%s: camera answered %s", m.Name, resp.Status)
		return "", fmt.Errorf("download %s: %s", m.Name, resp.Status)
	}
	total := m.Size
	if total == 0 && resp.ContentLength > 0 {
		total = offset + resp.ContentLength
	}

	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return "", err
	}
	done := offset
	buf := make([]byte, 1<<20)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				diag.Logf("download", "%s: writing to disk failed at %d bytes: %v", m.Name, done, werr)
				f.Close()
				return "", werr
			}
			done += int64(n)
			progress(done, total)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			diag.Logf("download", "%s: stopped at %d of %d bytes: %v", m.Name, done, total, rerr)
			f.Close()
			return "", rerr
		}
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(part, final); err != nil {
		diag.Logf("download", "%s: could not rename the .part file: %v", m.Name, err)
		return "", err
	}
	secs := time.Since(start).Seconds()
	diag.Logf("download", "saved %s: %d bytes in %.1fs (%.1f MB/s) -> %s", m.Name, done-offset, secs, float64(done-offset)/1e6/max(secs, 0.001), final)
	if !m.Created.IsZero() {
		_ = os.Chtimes(final, m.Created, m.Created)
	}
	return final, nil
}
