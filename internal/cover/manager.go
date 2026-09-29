package cover

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // cache file names only
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // Apple artwork is JPEG
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// maxSide is the largest side, in pixels, of a stored cover. The terminal
// scales the image into the placement area.
const maxSide = 512

// keep is how many placed images the terminal holds before the oldest is
// deleted.
const keep = 24

type placement struct {
	url        string
	cols, rows int
}

// Manager loads covers and tracks the images that the terminal holds.
// Load is safe for concurrent use; the other methods belong to the TUI
// goroutine.
type Manager struct {
	CellW, CellH int

	dir    string
	client *http.Client

	mu     sync.Mutex
	pngs   map[string][]byte // url → PNG
	failed map[string]bool

	placed map[placement]uint32
	order  []placement // oldest first
	used   map[uint32]bool
	next   uint32
}

// New returns a manager that caches covers in dir.
func New(dir string, cellW, cellH int) *Manager {
	return &Manager{
		CellW: cellW, CellH: cellH,
		dir:    dir,
		client: &http.Client{Timeout: 20 * time.Second},
		pngs:   map[string][]byte{},
		failed: map[string]bool{},
		placed: map[placement]uint32{},
		used:   map[uint32]bool{},
		next:   firstID,
	}
}

// Box returns the cover size in cells for a column width and the height
// that is available: a square in pixels, one cell of margin at each side,
// at most half the height. ok is false when the cover would be too small.
func (m *Manager) Box(width, height int) (cols, rows int, ok bool) {
	cols = min(width-2, MaxCells)
	if cols < 4 || m.CellW <= 0 || m.CellH <= 0 {
		return 0, 0, false
	}
	rows = (cols*m.CellW + m.CellH/2) / m.CellH
	if maxRows := height / 2; rows > maxRows {
		rows = maxRows
		cols = min((rows*m.CellH+m.CellW/2)/m.CellW, width-2)
	}
	if rows < 4 {
		return 0, 0, false
	}
	return cols, rows, true
}

// Has reports whether the cover of url is loaded.
func (m *Manager) Has(url string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pngs[url] != nil
}

// Failed reports whether the cover of url could not be loaded.
func (m *Manager) Failed(url string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failed[url]
}

func (m *Manager) cachePath(url string) string {
	sum := sha1.Sum([]byte(url)) //nolint:gosec // cache key
	return filepath.Join(m.dir, hex.EncodeToString(sum[:])+".png")
}

// Load gets the cover of url from the disk cache or downloads, scales, and
// caches it.
func (m *Manager) Load(ctx context.Context, url string) error {
	data, err := m.load(ctx, url)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.failed[url] = true
		return err
	}
	m.pngs[url] = data
	return nil
}

func (m *Manager) load(ctx context.Context, url string) ([]byte, error) {
	path := m.cachePath(url)
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 { //nolint:gosec // our cache
		return b, nil
	}
	src, err := m.open(ctx, url)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("cover: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, scaleDown(img, maxSide)); err != nil {
		return nil, err
	}
	if os.MkdirAll(m.dir, 0o750) == nil {
		_ = os.WriteFile(path, buf.Bytes(), 0o600)
	}
	return buf.Bytes(), nil
}

// open returns the image data of url: an http(s) URL, "file:" and a path
// (an image file), or "embedded:" and a path (the picture embedded in an
// audio file, extracted with ffmpeg).
func (m *Manager) open(ctx context.Context, url string) ([]byte, error) {
	if path, ok := strings.CutPrefix(url, "file:"); ok {
		return os.ReadFile(path) //nolint:gosec // an image in the music folder
	}
	if path, ok := strings.CutPrefix(url, "embedded:"); ok {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		// stdin and stderr stay nil (/dev/null), away from the TUI.
		cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", path, "-map", "0:v:0", "-frames:v", "1", //nolint:gosec // our paths
			"-f", "image2pipe", "-c:v", "png", "-")
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			return nil, fmt.Errorf("cover: no embedded picture in %s", path)
		}
		return out, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req) //nolint:gosec // artwork URL from Apple
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// Place returns the image id for the cover of url at cols×rows cells, and
// the escape sequences to send first: the transmission when this size is
// new, and deletions of evicted images. The cover must be loaded.
func (m *Manager) Place(url string, cols, rows int) (id uint32, seq string) {
	key := placement{url, cols, rows}
	if id, ok := m.placed[key]; ok {
		m.touch(key)
		return id, ""
	}
	m.mu.Lock()
	data := m.pngs[url]
	m.mu.Unlock()
	if data == nil {
		return 0, ""
	}
	var evict string
	for len(m.order) >= keep {
		old := m.order[0]
		m.order = m.order[1:]
		oldID := m.placed[old]
		delete(m.placed, old)
		delete(m.used, oldID)
		evict += Delete(oldID)
	}
	id = m.allocID()
	m.placed[key] = id
	m.used[id] = true
	m.order = append(m.order, key)
	return id, evict + Transmit(id, data, cols, rows)
}

func (m *Manager) touch(key placement) {
	for i, k := range m.order {
		if k == key {
			m.order = append(append(m.order[:i:i], m.order[i+1:]...), key)
			return
		}
	}
}

func (m *Manager) allocID() uint32 {
	for {
		id := m.next
		m.next++
		if m.next > lastID {
			m.next = firstID
		}
		if !m.used[id] {
			return id
		}
	}
}

// Cleanup returns the escape sequences that delete all placed images, for
// use when the program ends.
func (m *Manager) Cleanup() string {
	var b bytes.Buffer
	for _, id := range m.placed {
		b.WriteString(Delete(id))
	}
	return b.String()
}

// scaleDown returns img with its longer side at most maxSide pixels, by
// averaging the source pixels of each target pixel (a box filter).
func scaleDown(img image.Image, maxSide int) image.Image {
	sb := img.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw <= maxSide && sh <= maxSide {
		return img
	}
	dw, dh := maxSide, sh*maxSide/sw
	if sh > sw {
		dw, dh = sw*maxSide/sh, maxSide
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		y0, y1 := sb.Min.Y+y*sh/dh, sb.Min.Y+(y+1)*sh/dh
		for x := range dw {
			x0, x1 := sb.Min.X+x*sw/dw, sb.Min.X+(x+1)*sw/dw
			var r, g, b, a, n uint64
			for sy := y0; sy < max(y1, y0+1); sy++ {
				for sx := x0; sx < max(x1, x0+1); sx++ {
					cr, cg, cb, ca := img.At(sx, sy).RGBA()
					r, g, b, a, n = r+uint64(cr), g+uint64(cg), b+uint64(cb), a+uint64(ca), n+1
				}
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(b / n >> 8), uint8(a / n >> 8)})
		}
	}
	return dst
}
