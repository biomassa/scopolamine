package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/biomassa/scopolamine/internal/library"
	"github.com/biomassa/scopolamine/internal/player"
)

// barHeight is the now-playing area: separator, track line, progress line,
// and the legend lines (one, or two when the legend does not fit).
func (m *Model) barHeight() int { return 3 + len(m.legendLines()) }

func (m *Model) listHeight() int { return max(1, m.height-m.barHeight()-1) }

// modeLabel names the mode that shows: "Apple Music", "local · metadata",
// or "local · folders".
func (m *Model) modeLabel() string {
	if m.source == library.SourceLocal {
		return "local · " + m.sortName()
	}
	return "Apple Music"
}

// legendItem is a key of the legend and what it does. An item without a
// key (a hint) shows only its label.
type legendItem struct{ key, label string }

func (it legendItem) width() int {
	if it.key == "" {
		return ansi.StringWidth(it.label)
	}
	return ansi.StringWidth(it.key) + 1 + ansi.StringWidth(it.label)
}

// render draws the item as in godoist: the key in the theme accent, bold;
// the label muted.
func (it legendItem) render() string {
	if it.key == "" {
		return stMuted.Render(it.label)
	}
	return stLegendKey.Render(it.key) + " " + stMuted.Render(it.label)
}

func keys(pairs ...string) []legendItem {
	out := make([]legendItem, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, legendItem{pairs[i], pairs[i+1]})
	}
	return out
}

// legendItems are the keys of the legend for the current view.
func (m *Model) legendItems() []legendItem {
	switch {
	case m.filtering:
		return append([]legendItem{{"", "type to filter"}}, keys("enter", "play", "esc", "clear")...)
	case m.mode == modeSearch:
		return keys("enter", "play", "a", "add", "D", "remove", "s", "edit search", "esc", "library", "space", "pause",
			"←/→", "seek", "+/-", "vol", "T", "theme", "?", "help")
	case m.source == library.SourceLocal:
		return keys("enter", "play", "space", "pause", "[ ]", "track", "←/→", "seek", "+/-", "vol", "/", "filter",
			"v", "sort", "L", "Apple Music", "o", "playing", "T", "theme", "?", "help")
	}
	items := keys("enter", "play", "space", "pause", "[ ]", "track", "←/→", "seek", "+/-", "vol", "/", "filter",
		"s", "search", "D", "remove")
	if m.deps.ScanLocal != nil {
		items = append(items, legendItem{"L", "local"})
	}
	return append(items, keys("o", "playing", "T", "theme", "?", "help")...)
}

// legendLines lays the legend items out on one line, or on two when they do
// not fit, as in godoist. Items that do not fit on the first line start the
// second one.
func (m *Model) legendLines() [][]legendItem {
	const sepW = 3 // " · "
	width := max(10, m.width-1)
	lines := [][]legendItem{nil}
	w := 0
	for _, it := range m.legendItems() {
		cur := len(lines) - 1
		switch {
		case len(lines[cur]) == 0:
			lines[cur], w = append(lines[cur], it), it.width()
		case w+sepW+it.width() <= width || len(lines) == 2:
			lines[cur], w = append(lines[cur], it), w+sepW+it.width() // the second line is cut by fit
		default:
			lines, w = append(lines, []legendItem{it}), it.width()
		}
	}
	return lines
}

// legendText draws one legend line.
func legendText(items []legendItem) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = it.render()
	}
	return strings.Join(parts, stDim.Render(" · "))
}

// --- labels ---------------------------------------------------------------

func albumLabels(a []library.Album, withArtist bool) []string {
	out := make([]string, len(a))
	for i, al := range a {
		l := al.Title
		if withArtist {
			l = al.Artist + " — " + al.Title
		}
		if al.Year > 0 {
			l = strconv.Itoa(al.Year) + "  " + l
		} else {
			l = "····  " + l
		}
		out[i] = l
	}
	return out
}

// trackNumbers formats each track's number as "7." (or "2-7." on multi-disc
// albums), right-aligned to a common width.
func trackNumbers(t []library.Track) []string {
	multi := map[string]bool{}
	for _, tr := range t {
		if tr.Disc > 1 {
			multi[tr.AlbumID] = true
		}
	}
	out := make([]string, len(t))
	width := 0
	for i, tr := range t {
		n := strconv.Itoa(tr.Number)
		if tr.Number <= 0 {
			n = "·"
		}
		if multi[tr.AlbumID] {
			n = strconv.Itoa(max(1, tr.Disc)) + "-" + n
		}
		out[i] = n + "."
		width = max(width, len(out[i]))
	}
	for i := range out {
		out[i] = strings.Repeat(" ", width-len(out[i])) + out[i]
	}
	return out
}

func totalDuration(t []library.Track) time.Duration {
	var d time.Duration
	for _, tr := range t {
		d += tr.Duration
	}
	return d
}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Round(time.Second).Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// fit truncates or pads s (which may contain ANSI styling) to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

// leftRight lays out l and r in w cells with r right-aligned; l is truncated
// first.
func leftRight(l, r string, w int) string {
	rw := lipgloss.Width(r)
	if rw >= w {
		return fit(r, w)
	}
	return fit(l, w-rw) + r
}

// --- view -----------------------------------------------------------------

func (m *Model) View() tea.View {
	screen := m.render()
	if m.themes != nil {
		box, w := m.themeBox()
		screen = overlay(screen, box, max(0, m.width-w-1), 1)
	}
	v := tea.NewView(screen)
	v.AltScreen = true
	if themeBg != "" { // a palette theme sets the terminal background while scopolamine runs
		v.BackgroundColor = lipgloss.Color(themeBg)
	}
	v.WindowTitle = "scopolamine"
	if t := m.state.Track; t != nil && m.state.Playing {
		v.WindowTitle = t.Title + " — " + t.Artist + " · scopolamine"
	}
	return v
}

func (m *Model) render() string {
	if m.width < 20 || m.height < m.barHeight()+3 {
		return "terminal too small"
	}
	if m.showHelp {
		return m.renderHelp()
	}
	if m.mode == modeSearch && m.search != nil {
		return m.renderSearch()
	}
	sep := stSep.Render("│")
	w0, w1, w2 := m.columnWidths()
	widths := [numPanes]int{w0, w1, w2}
	h := m.listHeight()

	cols := [numPanes][]string{}
	for i := range numPanes {
		cols[i] = m.renderPane(i, widths[i], h)
	}
	var b strings.Builder
	for row := 0; row < h+1; row++ {
		b.WriteString(cols[0][row])
		b.WriteString(sep)
		b.WriteString(cols[1][row])
		b.WriteString(sep)
		b.WriteString(cols[2][row])
		b.WriteByte('\n')
	}
	b.WriteString(m.renderBar())
	return b.String()
}

// cell is one rendered row of a column.
type cell struct {
	left, right string
	playing     bool // gets the ▶ mark and accent colour
	paused      bool // with playing: ‖ instead of ▶
	header      bool
	dim         bool
	rightDim    bool // the right text is pale
	all         bool // an "All" row: accent color, pinned at the top
}

// renderColumn returns h+1 lines (title + h rows) of p, each exactly w cells.
// cellAt renders the row for an underlying item index.
func (m *Model) renderColumn(p *pane, w, h int, focused, filtering bool, empty string, cellAt func(idx int) cell) []string {
	title := " " + p.title
	count := fmt.Sprintf("%d ", len(p.match))
	if p.filter != "" || (focused && filtering) {
		title += "  /" + p.filter
		if focused && filtering {
			title += "▏"
		}
	}
	ts := stTitle
	if focused {
		ts = stTitleFocus
	}
	blank := strings.Repeat(" ", w)
	lines := []string{ts.Render(leftRight(title, count, w))}
	if h <= 0 {
		return lines
	}
	// An empty line under the title.
	lines = append(lines, blank)
	avail := h - 1

	row := func(mi int) string {
		c := cellAt(p.match[mi])
		return m.renderCell(c, w, mi == p.cursor, focused)
	}

	// The "All" row stays at the top, with an empty line under it; the list
	// below scrolls on its own.
	first, cursor := 0, p.cursor
	if len(p.match) > 0 && avail >= 3 && cellAt(p.match[0]).all {
		lines = append(lines, row(0), blank)
		avail -= 2
		first, cursor = 1, p.cursor-1
	}
	n := len(p.match) - first
	off := p.offset
	if cursor >= 0 {
		if cursor < off {
			off = cursor
		}
		if cursor >= off+avail {
			off = cursor - avail + 1
		}
	}
	off = max(0, min(off, max(0, n-avail)))
	p.offset = off
	for r := 0; r < avail; r++ {
		i := off + r
		if i >= n {
			if r == 0 && empty != "" {
				lines = append(lines, stDim.Render(fit(" "+empty, w)))
			} else {
				lines = append(lines, blank)
			}
			continue
		}
		lines = append(lines, row(first+i))
	}
	return lines
}

// renderCell draws one row of width w.
func (m *Model) renderCell(c cell, w int, cursor, focused bool) string {
	mark := "  "
	if c.playing {
		mark = "▶ "
		if c.paused {
			mark = "‖ "
		}
	}
	right := " " + c.right + " "
	st := stRow
	switch {
	case cursor && focused:
		st = stSelFocus
	case cursor:
		st = stSel
	case c.playing:
		st = stPlaying
	case c.all:
		st = stPlaying // the "All" rows use the accent
	case c.dim:
		st = stDim
	case c.header:
		st = stHeader
	}
	if c.rightDim && c.right != "" && ansi.StringWidth(right) < w {
		pale := stDim
		switch {
		case cursor && focused:
			pale = stSelFocusPale
		case cursor:
			pale = stSelPale
		}
		return st.Render(fit(mark+c.left, w-ansi.StringWidth(right))) + pale.Render(right)
	}
	return st.Render(leftRight(mark+c.left, right, w))
}

// trackCell renders row idx of a tracks column (shared by library and
// search views).
func (m *Model) trackCell(rows []trackRow, tracks []library.Track, nums []string, grouped bool, idx int) cell {
	row := rows[idx]
	switch row.kind {
	case rowAll:
		return cell{left: "All", right: fmt.Sprintf("%d · %s", len(tracks), fmtDur(totalDuration(tracks))), all: true}
	case rowHeader:
		year := "····"
		if row.album.Year > 0 {
			year = strconv.Itoa(row.album.Year)
		}
		c := cell{left: year + "  " + row.album.Title, header: true, playing: row.album.ID == m.playingAlbum.ID}
		if !albumPlayable(tracks, row.album.ID) {
			c.right, c.dim = "unavailable", true
		}
		return c
	}
	t := tracks[row.track]
	c := cell{left: nums[row.track] + " " + t.Title, right: fmtDur(t.Duration)}
	if grouped {
		c.left = "  " + c.left
	}
	if t.Artist != "" && library.ArtistKey(t.Artist) != library.ArtistKey(row.album.Artist) {
		c.left += " · " + t.Artist
	}
	if !t.Playable {
		c.right, c.dim = "unavailable", true
	}
	if m.state.Track != nil && t.ID == m.state.Track.ID {
		c.playing, c.paused = true, !m.state.Playing
	}
	return c
}

// renderPane renders library column i.
func (m *Model) renderPane(i, w, h int) []string {
	p := m.panes[i]
	focused := i == m.focus
	var empty string
	var cellAt func(int) cell
	switch i {
	case paneArtists:
		if len(m.artists) == 0 {
			empty = "library is empty"
			if m.syncing {
				empty = "syncing library… " + m.syncProgress()
			}
		}
		cellAt = func(idx int) cell {
			if idx == 0 {
				return cell{left: allArtists, all: true}
			}
			a := m.artists[idx-1]
			return cell{left: a.Name, right: strconv.Itoa(a.AlbumCount),
				playing: m.playingAlbum.ID != "" && library.ArtistKey(a.Name) == library.ArtistKey(m.playingAlbum.Artist)}
		}
	case paneAlbums:
		cellAt = func(idx int) cell {
			if idx == 0 {
				return cell{left: p.labels[0], right: strconv.Itoa(len(m.albums)), all: true}
			}
			a := m.albums[idx-1]
			return cell{left: p.labels[idx], right: a.Format, rightDim: true, playing: a.ID == m.playingAlbum.ID}
		}
	case paneTracks:
		switch {
		case m.tracksErr != nil:
			empty = "could not load tracks"
		case m.tracksWant != "" && m.tracksFor != m.tracksWant:
			empty = "loading…"
		case m.tracksWant == "" && m.allAlbumsSelected():
			empty = "pick an artist to list all of its tracks"
		}
		nums := trackNumbers(m.tracks)
		grouped := strings.HasPrefix(m.tracksFor, artistKeyPrefix)
		cellAt = func(idx int) cell { return m.trackCell(m.trackRows, m.tracks, nums, grouped, idx) }
		return m.withCover(w, h, func(h int) []string {
			return m.renderColumn(p, w, h, focused, m.filtering, empty, cellAt)
		})
	}
	return m.renderColumn(p, w, h, focused, m.filtering, empty, cellAt)
}

func (m *Model) renderBar() string {
	w := m.width
	var b strings.Builder
	b.WriteString(stSep.Render(strings.Repeat("─", w)))
	b.WriteByte('\n')

	s := m.state
	resuming := false
	if r := m.resume; s.Track == nil && r != nil && r.track != nil {
		// Show last session's track, paused, until space resumes it.
		resuming = true
		s.Position = r.pos
		s.Track = &player.NowPlaying{ID: r.track.ID, Title: r.track.Title, Artist: r.album.Artist,
			Album: r.album.Title, Duration: r.track.Duration}
	}
	// Line 1: what is playing.
	if t := s.Track; resuming {
		line := " " + stPlaying.Render("‖") + " " + stBold.Render(t.Title) +
			stDim.Render("  ·  ") + t.Artist + stDim.Render("  ·  ") + t.Album + stDim.Render("  — space resumes")
		b.WriteString(fit(line, w))
	} else if t != nil {
		icon := "▶"
		switch {
		case s.Loading:
			icon = "…"
		case !s.Playing:
			icon = "‖"
		}
		pos := ""
		if s.QueueLength > 0 && s.QueueIndex >= 0 {
			pos = fmt.Sprintf("  %d/%d", s.QueueIndex+1, s.QueueLength)
		}
		line := " " + stPlaying.Render(icon) + " " + stBold.Render(t.Title) +
			stDim.Render("  ·  ") + t.Artist + stDim.Render("  ·  ") + t.Album + stDim.Render(pos)
		b.WriteString(fit(line, w))
	} else {
		b.WriteString(fit(stDim.Render(" ■ stopped"), w))
	}
	b.WriteByte('\n')

	// Line 2: progress.
	var dur time.Duration
	if s.Track != nil {
		dur = s.Track.Duration
	}
	elapsed, total := fmtDur(s.Position), fmtDur(dur)
	// The mode that shows, then the format of what plays.
	label := m.modeLabel()
	if s.Track != nil && s.Format != "" && !resuming {
		label += " · " + s.Format
	}
	right := fmt.Sprintf("  %s  %s  vol %d%% ", total, label, int(m.volume*100+0.5))
	left := " " + elapsed + "  "
	barW := max(0, w-lipgloss.Width(left)-lipgloss.Width(right))
	filled := 0
	if dur > 0 {
		filled = int(float64(barW) * min(1, max(0, float64(s.Position)/float64(dur))))
	}
	bar := stPlaying.Render(strings.Repeat("━", filled)) + stSep.Render(strings.Repeat("─", barW-filled))
	b.WriteString(fit(left+bar+stDim.Render(right), w))
	b.WriteByte('\n')

	// Lines 3+: the legend, or a notice or status in the first legend line.
	legend := m.legendLines()
	var status string
	switch {
	case m.confirm != nil:
		status = stTitleFocus.Render(" " + m.confirm.prompt)
	case m.notice != "" && m.noticeErr:
		status = stErr.Render(" " + m.notice)
	case m.notice != "":
		status = " " + m.notice
	case m.syncing:
		status = " syncing library… " + m.syncProgress()
		if m.playerStatus != "" {
			status += stDim.Render("  ·  " + m.playerStatus)
		}
	case m.playerStatus != "":
		status = stDim.Render(" " + m.playerStatus)
	}
	for i, l := range legend {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch {
		case i == 0 && status != "":
			b.WriteString(fit(status, w))
		case status != "":
			b.WriteString(strings.Repeat(" ", w)) // keep the bar height
		default:
			b.WriteString(fit(" "+legendText(l), w))
		}
	}
	return b.String()
}

func (m *Model) renderHelp() string {
	rows := [][2]string{
		{"tab / l", "next column"},
		{"shift+tab / h", "previous column"},
		{"1 2 3", "focus artists / albums / tracks"},
		{"j k ↑ ↓", "move"},
		{"g G  pgup pgdn", "top / bottom / page"},
		{"/", "filter the focused column (esc clears)"},
		{"enter", "play album (from the selected track in Tracks)"},
		{"space", "play / pause (after a restart: resume where you left off)"},
		{"] [  (n p)", "next / previous track"},
		{"→ ←  (. ,)", "seek ±10 s"},
		{"shift+→ shift+←", "seek ±60 s"},
		{"+  -", "volume"},
		{"x", "stop"},
		{"o", "jump to what is playing"},
		{"R", "sync the Apple Music library, or scan the local folder"},
		{"s", "search Apple Music (esc returns to the library)"},
		{"a", "in search: add the album to your library"},
		{"D", "remove the album from your library (asks first)"},
		{"T", "choose a color theme (live preview, enter keeps it)"},
		{"L", "switch between Apple Music and the local library"},
		{"v", "local library: sort by metadata or by folders"},
		{"q  ctrl+c", "quit"},
	}
	var b strings.Builder
	b.WriteString("\n " + stTitleFocus.Render("scopolamine "+m.deps.Version+" — keys") + "\n\n")
	for _, r := range rows {
		b.WriteString("  " + stPlaying.Render(fit(r[0], 20)) + stRow.Render(r[1]) + "\n")
	}
	b.WriteString("\n " + stDim.Render("Playback: Apple Music via MusicKit JS in headless Chrome (Widevine), 256 kbps AAC.") + "\n")
	b.WriteString(" " + stDim.Render("press any key") + "\n")
	return b.String()
}

func (m *Model) syncProgress() string {
	switch {
	case m.syncOf > 0:
		return fmt.Sprintf("%d/%d albums", m.syncN, m.syncOf)
	case m.syncN > 0:
		return fmt.Sprintf("%d albums", m.syncN)
	}
	return ""
}

// albumPlayable reports whether any of tracks from album id can be played.
func albumPlayable(tracks []library.Track, id string) bool {
	for _, t := range tracks {
		if t.AlbumID == id && t.Playable {
			return true
		}
	}
	return false
}
