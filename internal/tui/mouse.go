package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/biomassa/scopolamine/internal/player"
)

// doubleClick is the longest time between the two clicks of a double click.
const doubleClick = 400 * time.Millisecond

// span is a part of one screen line: line y, columns x0 to x1 (exclusive).
type span struct{ y, x0, x1 int }

func (s span) has(x, y int) bool { return y == s.y && x >= s.x0 && x < s.x1 }

// hitMap is where the last drawn screen has its parts, for mouse clicks.
type hitMap struct {
	cols   []colHit
	barTop int           // the first line of the status bar
	bar    span          // the progress bar (y 0: none)
	barDur time.Duration // the length of the track of the progress bar
	legend []keyHit
	box    span // the open box (theme picker, folder box): its top line
	boxH   int  // and its height
	prompt span // the search line of the search view (y 0 and x1 0: none)
}

type colHit struct {
	p      *pane
	idx    int // paneArtists, paneAlbums, paneTracks
	x0, w  int
	y0     int
	search bool
}

// keyHit is a legend key on the screen and the key it pushes.
type keyHit struct {
	span
	key string
}

func (m *Model) recordColumns(y0 int, widths [numPanes]int, panes [numPanes]*pane) {
	x := 0
	for i := range numPanes {
		m.hits.cols = append(m.hits.cols, colHit{p: panes[i], idx: i, x0: x, w: widths[i], y0: y0, search: y0 > 0})
		x += widths[i] + 1 // the separator
	}
}

// recordLegend notes the keys of a legend line. A key with two actions
// ("[ ]", "+/-", "←/→") has one per symbol; the label does nothing there.
// Other items push their key from the whole item.
func (m *Model) recordLegend(y int, items []legendItem) {
	x := 1 // the line starts with a space
	for _, it := range items {
		w := it.width()
		switch it.key {
		case "":
		case "[ ]":
			m.hits.legend = append(m.hits.legend, keyHit{span{y, x, x + 1}, "["}, keyHit{span{y, x + 2, x + 3}, "]"})
		case "+/-":
			m.hits.legend = append(m.hits.legend, keyHit{span{y, x, x + 1}, "+"}, keyHit{span{y, x + 2, x + 3}, "-"})
		case "←/→":
			m.hits.legend = append(m.hits.legend, keyHit{span{y, x, x + 1}, "left"}, keyHit{span{y, x + 2, x + 3}, "right"})
		default:
			m.hits.legend = append(m.hits.legend, keyHit{span{y, x, x + w}, it.key})
		}
		x += w + 3 // " · "
	}
}

// keyPress builds the key press of a key name, as keyName reads it back.
func keyPress(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

// lastClick is the previous click, for double clicks.
type lastClick struct {
	at   time.Time
	x, y int
}

func (m *Model) isDouble(x, y int) bool {
	now := time.Now()
	d := m.click.x == x && m.click.y == y && now.Sub(m.click.at) <= doubleClick
	m.click = lastClick{at: now, x: x, y: y}
	if d {
		m.click = lastClick{} // a third click starts again
	}
	return d
}

// onClick handles a left click.
func (m *Model) onClick(ev tea.Mouse) tea.Cmd {
	if ev.Button != tea.MouseLeft || m.quitting {
		return nil
	}
	x, y := ev.X, ev.Y
	double := m.isDouble(x, y)
	switch {
	case m.showHelp:
		m.showHelp = false
		return nil
	case m.themes != nil:
		return m.clickThemes(x, y, double)
	case m.folder != nil:
		return m.clickFolder(x, y)
	case m.confirm != nil:
		return nil
	}
	if m.hits.bar.has(x, y) {
		f := float64(x-m.hits.bar.x0) / float64(max(1, m.hits.bar.x1-m.hits.bar.x0))
		pos := time.Duration(f * float64(m.hits.barDur))
		m.state.Position = pos
		return m.withPlayer(func(p player.Player) error { return p.Seek(pos) })
	}
	for _, k := range m.hits.legend {
		if k.has(x, y) {
			return m.handleKey(keyPress(k.key))
		}
	}
	if s := m.search; s != nil && m.mode == modeSearch && m.hits.prompt.has(x, y) {
		s.editing = true // a click on the search line edits the search again
		return nil
	}
	c, mi, ok := m.colAt(x, y)
	if !ok {
		return nil
	}
	m.filtering = false
	if s := m.search; c.search && s != nil {
		s.editing = false
		s.focus = c.idx
	} else {
		m.focus = c.idx
	}
	if mi < 0 {
		if y == c.y0 && !c.search && c.p.filter != "" {
			m.filtering = true // a click on the title of a filtered column edits the filter
		}
		return nil // the title or an empty line: only the focus
	}
	c.p.cursor = mi
	cmd := m.afterMove(c)
	if !double {
		return cmd
	}
	if m.holdsPlaying(c) {
		return tea.Batch(cmd, m.withPlayer(player.Player.Toggle))
	}
	return tea.Batch(cmd, m.handleKey(keyPress("enter")))
}

// onWheel moves the cursor of the column under the mouse.
func (m *Model) onWheel(ev tea.Mouse) tea.Cmd {
	d := 0
	switch ev.Button {
	case tea.MouseWheelUp:
		d = -1
	case tea.MouseWheelDown:
		d = 1
	default:
		return nil
	}
	if m.themes != nil {
		if d < 0 {
			return m.handleKey(keyPress("up"))
		}
		return m.handleKey(keyPress("down"))
	}
	if m.folder != nil || m.showHelp || m.confirm != nil || m.quitting {
		return nil
	}
	c, _, ok := m.colAt(ev.X, ev.Y)
	if !ok {
		return nil
	}
	if s := m.search; c.search && s != nil {
		s.editing = false
		s.focus = c.idx
	} else {
		m.filtering = false
		m.focus = c.idx
	}
	c.p.move(d)
	return m.afterMove(c)
}

// colAt finds the column at x, y and the match index of the row there (-1:
// no row).
func (m *Model) colAt(x, y int) (colHit, int, bool) {
	for _, c := range m.hits.cols {
		if x < c.x0 || x >= c.x0+c.w || y < c.y0 {
			continue
		}
		line := y - c.y0
		if line >= len(c.p.lineRows) {
			return c, -1, true
		}
		return c, c.p.lineRows[line], true
	}
	return colHit{}, -1, false
}

// afterMove updates the columns after the cursor of c moved, as the keys do.
func (m *Model) afterMove(c colHit) tea.Cmd {
	if c.search {
		switch c.idx {
		case paneArtists:
			return m.searchArtistChanged()
		case paneAlbums:
			return m.searchAlbumChanged()
		}
		return nil
	}
	return m.selectionMoved()
}

// holdsPlaying reports whether the row under the cursor of c holds the
// track that plays or is paused.
func (m *Model) holdsPlaying(c colHit) bool {
	st := m.state
	if st.Track == nil {
		return false
	}
	if c.search {
		s := m.search
		switch c.idx {
		case paneAlbums:
			a, ok := s.selectedAlbum()
			return ok && a.ID == m.playingAlbum.ID
		case paneTracks:
			i := s.panes[paneTracks].selected()
			return i >= 0 && i < len(s.trackRows) && s.trackRows[i].kind == rowTrack && s.tracks[s.trackRows[i].track].ID == st.Track.ID
		}
		return false
	}
	if c.idx == paneArtists || m.playingView() != m.libView {
		return false
	}
	id, albumID, kind := m.currentTrack()
	return kind != curResume && m.rowHolds(m.tracksKey(), id, albumID)
}

// clickThemes: a click on a theme previews it, a double click keeps it, and
// a click outside the box cancels.
func (m *Model) clickThemes(x, y int, double bool) tea.Cmd {
	b := m.hits.box
	if y < b.y || y >= b.y+m.hits.boxH || x < b.x0 || x >= b.x1 {
		return m.handleKey(keyPress("esc"))
	}
	p := m.themes
	i := y - b.y - 2 + p.off // the border and a blank line come first
	if i < p.off || i >= len(p.names) || i >= p.off+p.rows {
		return nil
	}
	p.cur = i
	ApplyTheme(p.names[i])
	if double {
		return m.handleKey(keyPress("enter"))
	}
	return nil
}

// clickFolder: a click on a listed folder puts it into the path, and a
// click outside the box cancels.
func (m *Model) clickFolder(x, y int) tea.Cmd {
	b := m.hits.box
	if y < b.y || y >= b.y+m.hits.boxH || x < b.x0 || x >= b.x1 {
		m.folder = nil
		return nil
	}
	p := m.folder
	if y != b.y+3 || p.cands == nil || p.busy { // the line of the matches
		return nil
	}
	cx := b.x0 + 2 // the border and a space
	for i, c := range p.cands {
		w := ansi.StringWidth(c) + 1
		if x >= cx && x < cx+w {
			p.cur = i
			p.text = p.parent + c + "/"
			p.pos = len([]rune(p.text))
			return nil
		}
		cx += w + 2
	}
	return nil
}
