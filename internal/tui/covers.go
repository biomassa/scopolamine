package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/biomassa/scopolamine/internal/cover"
)

// coverView is the cover in the tracks column: which image, at which size.
type coverView struct {
	url        string
	id         uint32
	cols, rows int
}

type coverLoadedMsg struct {
	url string
	err error
}

// columnWidths returns the widths of the three columns.
func (m *Model) columnWidths() (w0, w1, w2 int) {
	w0 = max(12, m.width*22/100)
	w1 = max(16, m.width*36/100)
	return w0, w1, m.width - w0 - w1 - 2
}

// columnHeight is the number of list rows in a column (under the title).
func (m *Model) columnHeight() int {
	if m.mode == modeSearch {
		return m.listHeight() - 1 // the search line
	}
	return m.listHeight()
}

// coverURL is the artwork of the album that the tracks column shows. There
// is no cover for "All albums": only a single selected album has one.
func (m *Model) coverURL() string {
	if m.mode == modeSearch {
		if m.search == nil {
			return ""
		}
		if a, ok := m.search.shownAlbum(); ok {
			return a.ArtworkURL
		}
		return ""
	}
	if m.tracksFor == "" || m.tracksFor != m.tracksWant {
		return ""
	}
	if strings.HasPrefix(m.tracksFor, artistKeyPrefix) {
		return "" // "All albums"
	}
	for _, a := range m.albums {
		if a.ID == m.tracksFor {
			return a.ArtworkURL
		}
	}
	return ""
}

// coverBox is the cover size for the current layout.
func (m *Model) coverBox() (cols, rows int, ok bool) {
	if m.deps.Covers == nil || m.width == 0 {
		return 0, 0, false
	}
	_, _, w2 := m.columnWidths()
	return m.deps.Covers.Box(w2, m.columnHeight())
}

// syncCover makes the terminal hold the cover that the view shows: it
// starts a download, or sends the image once it is loaded. It runs after
// every update.
func (m *Model) syncCover() tea.Cmd {
	mgr := m.deps.Covers
	if mgr == nil {
		return nil
	}
	url := m.coverURL()
	cols, rows, ok := m.coverBox()
	if url == "" || !ok || mgr.Failed(url) {
		m.cover = nil
		return nil
	}
	if c := m.cover; c != nil && c.url == url && c.cols == cols && c.rows == rows {
		return nil
	}
	if !mgr.Has(url) {
		m.cover = nil
		if m.coverLoading[url] {
			return nil
		}
		if m.coverLoading == nil {
			m.coverLoading = map[string]bool{}
		}
		m.coverLoading[url] = true
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
			defer cancel()
			return coverLoadedMsg{url, mgr.Load(ctx, url)}
		}
	}
	id, seq := mgr.Place(url, cols, rows)
	m.cover = &coverView{url: url, id: id, cols: cols, rows: rows}
	if seq == "" {
		return nil
	}
	return tea.Raw(seq)
}

// withCover renders a tracks column of width w and h rows, with the cover
// at the bottom when there is one. render draws the list part for a
// height; it returns height+1 lines (title and rows).
func (m *Model) withCover(w, h int, render func(h int) []string) []string {
	url := m.coverURL()
	cols, rows, ok := m.coverBox()
	if url == "" || !ok || m.deps.Covers.Failed(url) {
		return render(h)
	}
	listH := h - rows - 1
	lines := render(listH)
	lines = append(lines, strings.Repeat(" ", w))
	c := m.cover
	if c == nil || c.url != url || c.cols != cols || c.rows != rows {
		for range rows {
			lines = append(lines, strings.Repeat(" ", w)) // loading
		}
		return lines
	}
	// Right-aligned, with the same one-cell margin as the track durations.
	left := max(0, w-cols-1)
	st := lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(int(c.id))))
	for _, ph := range cover.Placeholders(cols, rows) {
		lines = append(lines, strings.Repeat(" ", left)+st.Render(ph)+strings.Repeat(" ", w-cols-left))
	}
	return lines
}
