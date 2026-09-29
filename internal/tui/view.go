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

var (
	colAccent = lipgloss.Color("#d7af5f")
	colDim    = lipgloss.Color("#6c6c6c")
	colFg     = lipgloss.Color("#d0d0d0")
	colSelBg  = lipgloss.Color("#3a3a3a")
	colErr    = lipgloss.Color("#e06c75")

	stTitle      = lipgloss.NewStyle().Foreground(colDim).Bold(true)
	stTitleFocus = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	stRow        = lipgloss.NewStyle().Foreground(colFg)
	stDim        = lipgloss.NewStyle().Foreground(colDim)
	stSel        = lipgloss.NewStyle().Foreground(colFg).Background(colSelBg)
	stSelFocus   = lipgloss.NewStyle().Foreground(lipgloss.Color("#1c1c1c")).Background(colAccent).Bold(true)
	stPlaying    = lipgloss.NewStyle().Foreground(colAccent)
	stSep        = lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))
	stErr        = lipgloss.NewStyle().Foreground(colErr)
	stBold       = lipgloss.NewStyle().Foreground(colFg).Bold(true)
	stHeader     = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
)

// barHeight is the now-playing area: separator, track line, progress line,
// status/notice line.
const barHeight = 4

func (m *Model) listHeight() int { return max(1, m.height-barHeight-1) }

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
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "scopolamine"
	if t := m.state.Track; t != nil && m.state.Playing {
		v.WindowTitle = t.Title + " — " + t.Artist + " · scopolamine"
	}
	return v
}

func (m *Model) render() string {
	if m.width < 20 || m.height < barHeight+3 {
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
}

// renderColumn returns h+1 lines (title + h rows) of p, each exactly w cells.
// cellAt renders the row for an underlying item index.
func (m *Model) renderColumn(p *pane, w, h int, focused, filtering bool, empty string, cellAt func(idx int) cell) []string {
	p.scroll(h)
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
	lines := []string{ts.Render(leftRight(title, count, w))}
	for r := 0; r < h; r++ {
		mi := p.offset + r
		if mi >= len(p.match) {
			if r == 0 && empty != "" {
				lines = append(lines, stDim.Render(fit(" "+empty, w)))
			} else {
				lines = append(lines, strings.Repeat(" ", w))
			}
			continue
		}
		c := cellAt(p.match[mi])
		mark := "  "
		if c.playing {
			mark = "▶ "
			if c.paused {
				mark = "‖ "
			}
		}
		text := leftRight(mark+c.left, " "+c.right+" ", w)
		st := stRow
		switch {
		case mi == p.cursor && focused:
			st = stSelFocus
		case mi == p.cursor:
			st = stSel
		case c.playing:
			st = stPlaying
		case c.dim:
			st = stDim
		case c.header:
			st = stHeader
		}
		lines = append(lines, st.Render(text))
	}
	return lines
}

// trackCell renders row idx of a tracks column (shared by library and
// search views).
func (m *Model) trackCell(rows []trackRow, tracks []library.Track, nums []string, grouped bool, idx int) cell {
	row := rows[idx]
	switch row.kind {
	case rowAll:
		return cell{left: "All", right: fmt.Sprintf("%d · %s", len(tracks), fmtDur(totalDuration(tracks)))}
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
				return cell{left: allArtists}
			}
			a := m.artists[idx-1]
			return cell{left: a.Name, right: strconv.Itoa(a.AlbumCount),
				playing: m.playingAlbum.ID != "" && library.ArtistKey(a.Name) == library.ArtistKey(m.playingAlbum.Artist)}
		}
	case paneAlbums:
		cellAt = func(idx int) cell {
			if idx == 0 {
				return cell{left: p.labels[0], right: strconv.Itoa(len(m.albums))}
			}
			return cell{left: p.labels[idx], playing: m.albums[idx-1].ID == m.playingAlbum.ID}
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
	quality := "AAC 256"
	if s.BitrateKbps > 0 {
		quality = fmt.Sprintf("AAC %d", s.BitrateKbps)
	}
	right := fmt.Sprintf("  %s  %s  vol %d%% ", total, quality, int(m.volume*100+0.5))
	left := " " + elapsed + "  "
	barW := max(0, w-lipgloss.Width(left)-lipgloss.Width(right))
	filled := 0
	if dur > 0 {
		filled = int(float64(barW) * min(1, float64(s.Position)/float64(dur)))
	}
	bar := stPlaying.Render(strings.Repeat("━", filled)) + stSep.Render(strings.Repeat("─", barW-filled))
	b.WriteString(fit(left+bar+stDim.Render(right), w))
	b.WriteByte('\n')

	// Line 3: notices / status / key hint.
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
	case m.filtering:
		status = stDim.Render(" type to filter · enter play · esc clear")
	case m.mode == modeSearch:
		status = stDim.Render(" enter play · a add · D remove · s edit search · esc library · space pause · ←/→ seek · +/- vol · ? help")
	default:
		status = stDim.Render(" enter play · space pause · n/p track · ←/→ seek · +/- vol · / filter · s search · D remove · o playing · ? help")
	}
	b.WriteString(fit(status, w))
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
		{"n  p", "next / previous track"},
		{"→ ←  (. ,)", "seek ±10 s"},
		{"shift+→ shift+←", "seek ±60 s"},
		{"+  -", "volume"},
		{"x", "stop"},
		{"o", "jump to what is playing"},
		{"R", "re-sync library from Apple Music"},
		{"s", "search Apple Music (esc returns to the library)"},
		{"a", "in search: add the album to your library"},
		{"D", "remove the album from your library (asks first)"},
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
