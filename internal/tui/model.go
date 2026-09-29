// Package tui is the three-pane library browser: Artists │ Albums │ Tracks,
// with a now-playing bar. Albums are the unit of playback.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/cover"
	"github.com/biomassa/scopolamine/internal/library"
	"github.com/biomassa/scopolamine/internal/player"
)

const (
	paneArtists = iota
	paneAlbums
	paneTracks
	numPanes
)

const (
	allArtists = "All artists"
	allAlbums  = "All albums"
)

// Deps is what the model needs from the outside world. Src and Player may be
// nil (offline / playback unavailable); Player usually arrives later through
// a PlayerReadyMsg.
type Deps struct {
	Store  *library.Store
	Src    library.AppleSource
	Player player.Player
	// AutoSync syncs the album list on startup.
	AutoSync bool
	// Volume is the initial volume, 0..1.
	Volume float64
	// Resume is the previous session to restore (may be nil).
	Resume *Session
	// Catalog powers the Apple Music search view (nil: search disabled).
	Catalog Catalog
	// Version is shown on the help screen.
	Version string
	// Covers shows album covers in the tracks column (nil: no covers).
	Covers *cover.Manager
	// SaveTheme saves the theme that the picker keeps (nil: not saved).
	SaveTheme func(name string) error
}

// Messages sent in from outside (see cmd/scopolamine).
type (
	// PlayerStatusMsg is progress text while the player starts.
	PlayerStatusMsg string
	// PlayerReadyMsg hands over a started player.
	PlayerReadyMsg struct{ Player player.Player }
	// PlayerFailedMsg reports that playback could not start.
	PlayerFailedMsg struct{ Err error }
)

type (
	artistsMsg struct {
		artists []library.Artist
		err     error
	}
	albumsMsg struct {
		artist string
		albums []library.Album
		err    error
	}
	tracksMsg struct {
		key    string
		tracks []library.Track
		albums []library.Album
		err    error
	}
	syncDoneMsg struct {
		n   int
		err error
	}
	syncProgressMsg struct{ n, total int }
	stateMsg        struct {
		s  player.State
		ok bool
	}
	clearNoticeMsg struct{ seq int }
)

// Model is the Bubble Tea model.
type Model struct {
	deps Deps
	ctx  context.Context

	width, height int
	focus         int
	panes         [numPanes]*pane
	filtering     bool

	artists []library.Artist
	albums  []library.Album

	// Tracks pane: tracks is the flat, playable list for tracksFor (an
	// album id or artist key); trackRows is how it is displayed.
	tracks        []library.Track
	trackRows     []trackRow
	tracksFor     string
	tracksWant    string // key the pane should show (possibly still loading)
	tracksLoading string
	tracksErr     error
	pendingPlay   *pendingPlay

	albumsFor string // artist the albums slice belongs to

	// Selections to apply when the async lists arrive (session restore,
	// jump-to-playing).
	wantArtist string
	wantAlbum  string
	wantTrack  string

	resume      *resumePoint
	pendingSeek *seekTarget

	// queueAlbums maps queued track ids to their albums, so "what is
	// playing" is known even when the queue spans several albums.
	queueAlbums  map[string]library.Album
	playingAlbum library.Album
	state        player.State
	stateCh      tea.Cmd
	volume       float64
	playerStatus string

	syncing       bool
	syncCh        chan tea.Msg
	syncN, syncOf int
	notice        string
	noticeErr     bool
	noticeSeq     int
	showHelp      bool

	mode   int // modeLibrary or modeSearch
	search *searchState

	confirm  *confirmation
	removing map[string]bool // library album ids being removed

	cover        *coverView
	coverLoading map[string]bool

	themes *themePicker // the open theme picker, or nil
}

type pendingPlay struct {
	key     string // tracks key
	trackID string // "" = from the first track
}

// New builds the model.
func New(ctx context.Context, d Deps) *Model {
	m := &Model{deps: d, ctx: ctx, volume: d.Volume}
	if m.volume <= 0 {
		m.volume = 1
	}
	m.panes[paneArtists] = &pane{title: "Artists"}
	m.panes[paneAlbums] = &pane{title: "Albums"}
	m.panes[paneTracks] = &pane{title: "Tracks"}
	m.state.QueueIndex = -1
	m.applySession(d.Resume)
	if d.Player == nil {
		m.playerStatus = "starting player…"
	}
	return m
}

// Volume returns the current volume (for persisting on exit).
func (m *Model) Volume() float64 { return m.volume }

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadArtists(), m.loadResumeInfo()}
	if m.deps.AutoSync && m.deps.Src != nil {
		m.syncing = true
		cmds = append(cmds, m.syncCmd())
	}
	if m.deps.Player != nil {
		cmds = append(cmds, m.watchPlayer())
	}
	return tea.Batch(cmds...)
}

// --- commands -------------------------------------------------------------

func (m *Model) loadArtists() tea.Cmd {
	store := m.deps.Store
	return func() tea.Msg {
		a, err := store.Artists(m.ctx)
		return artistsMsg{a, err}
	}
}

func (m *Model) loadAlbums(artist string) tea.Cmd {
	store := m.deps.Store
	return func() tea.Msg {
		var (
			a   []library.Album
			err error
		)
		if artist == allArtists {
			a, err = store.AllAlbums(m.ctx)
		} else {
			a, err = store.AlbumsByArtist(m.ctx, artist)
		}
		return albumsMsg{artist, a, err}
	}
}

// syncCmd runs the album sync in the background. Progress and the final
// result arrive through one channel, read one message per command.
func (m *Model) syncCmd() tea.Cmd {
	store, src := m.deps.Store, m.deps.Src
	ch := make(chan tea.Msg, 1)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Minute)
		defer cancel()
		n, err := library.SyncAppleAlbums(ctx, store, src, func(n, total int) {
			select { // drop intermediate updates the UI hasn't read yet
			case ch <- syncProgressMsg{n, total}:
			default:
			}
		})
		ch <- syncDoneMsg{n, err}
	}()
	m.syncCh = ch
	return m.readSync()
}

func (m *Model) readSync() tea.Cmd {
	ch := m.syncCh
	return func() tea.Msg { return <-ch }
}

func (m *Model) watchPlayer() tea.Cmd {
	ch := m.deps.Player.Subscribe()
	next := func() tea.Msg {
		s, ok := <-ch
		return stateMsg{s, ok}
	}
	m.stateCh = next
	return next
}

func (m *Model) flash(msg string, isErr bool) tea.Cmd {
	m.noticeSeq++
	seq := m.noticeSeq
	m.notice, m.noticeErr = msg, isErr
	d := 4 * time.Second
	if isErr {
		d = 8 * time.Second
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return clearNoticeMsg{seq} })
}

// --- update ---------------------------------------------------------------

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.update(msg)
	if c := m.syncCover(); c != nil {
		cmd = tea.Batch(cmd, c)
	}
	return m, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.deps.Covers != nil {
			// The cell size changes with the font size.
			m.deps.Covers.CellW, m.deps.Covers.CellH = cover.CellSize()
		}
		return m, nil

	case coverLoadedMsg:
		delete(m.coverLoading, msg.url)
		return m, nil

	case tea.KeyPressMsg:
		return m, m.handleKey(msg)

	case artistsMsg:
		if msg.err != nil {
			return m, m.flash("library: "+msg.err.Error(), true)
		}
		return m, m.setArtists(msg.artists)

	case albumsMsg:
		if msg.err != nil {
			return m, m.flash("library: "+msg.err.Error(), true)
		}
		if msg.artist != m.selectedArtist() {
			return m, nil // stale
		}
		return m, m.setAlbums(msg.artist, msg.albums)

	case tracksLoadMsg:
		if msg.key != m.tracksWant || msg.key == m.tracksFor || msg.key == m.tracksLoading {
			return m, nil // cursor moved on, or already there
		}
		m.tracksLoading = msg.key
		return m, m.loadTracks(msg.key)

	case tracksMsg:
		return m, m.onTracks(msg)

	case syncProgressMsg:
		m.syncN, m.syncOf = msg.n, msg.total
		return m, m.readSync()

	case syncDoneMsg:
		m.syncing = false
		m.syncN, m.syncOf = 0, 0
		if msg.err != nil {
			return m, m.flash("sync failed: "+msg.err.Error(), true)
		}
		return m, tea.Batch(m.loadArtists(), m.flash(fmt.Sprintf("library synced: %d albums", msg.n), false))

	case PlayerStatusMsg:
		m.playerStatus = string(msg)
		return m, nil

	case PlayerReadyMsg:
		m.deps.Player = msg.Player
		m.playerStatus = ""
		_ = msg.Player.SetVolume(m.volume)
		return m, m.watchPlayer()

	case PlayerFailedMsg:
		m.playerStatus = "playback unavailable"
		return m, m.flash("player: "+msg.Err.Error(), true)

	case stateMsg:
		if !msg.ok {
			m.deps.Player = nil
			m.playerStatus = "player stopped"
			return m, nil
		}
		return m, tea.Batch(m.applyState(msg.s), m.stateCh)

	case resumeInfoMsg:
		m.onResumeInfo(msg)
		return m, nil

	case removedMsg:
		return m, m.onRemoved(msg)

	case themeSavedMsg:
		return m, m.onThemeSaved(msg)

	case searchDebounceMsg, searchResultMsg, searchAlbumsLoadMsg, searchAlbumsMsg,
		searchTracksLoadMsg, searchTracksMsg, addedMsg:
		return m, m.updateSearch(msg)

	case clearNoticeMsg:
		if msg.seq == m.noticeSeq {
			m.notice = ""
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) onTracks(msg tracksMsg) tea.Cmd {
	if msg.key == m.tracksLoading {
		m.tracksLoading = ""
	}
	var cmd tea.Cmd
	startID := ""
	if pp := m.pendingPlay; pp != nil && pp.key == msg.key {
		m.pendingPlay = nil
		if msg.err == nil {
			cmd = m.playTracks(msg.tracks, msg.albums, pp.trackID)
			startID = pp.trackID
			if startID == "" && len(msg.tracks) > 0 {
				startID = msg.tracks[0].ID
			}
		}
	}
	if msg.key != m.tracksWant {
		return cmd
	}
	m.tracksFor, m.tracks, m.tracksErr = msg.key, msg.tracks, msg.err
	m.trackRows = buildTrackRows(msg.tracks, msg.albums, strings.HasPrefix(msg.key, artistKeyPrefix))
	m.panes[paneTracks].setItems(m.trackRowLabels())
	switch {
	case m.wantTrack != "":
		m.selectTrackRow(m.wantTrack)
		m.wantTrack = ""
	case startID != "":
		m.selectTrackRow(startID)
	default:
		m.cursorToPlaying()
	}
	if msg.err != nil {
		return tea.Batch(cmd, m.flash("tracks: "+msg.err.Error(), true))
	}
	return cmd
}

func (m *Model) applyState(s player.State) tea.Cmd {
	var cmd tea.Cmd
	switch {
	case s.NeedsAuth:
		cmd = m.flash("Apple Music session expired — quit and run `scopolamine login`", true)
	case s.Error != "":
		cmd = m.flash(s.Error, true)
	case s.SkippedID != "":
		cmd = m.flash("skipped a track that is unavailable in your storefront", false)
	}
	m.state = s
	if s.Volume > 0 {
		m.volume = s.Volume
	}
	if s.Track != nil {
		m.resume = nil // this session is playing; the old resume point is moot
		if a, ok := m.queueAlbums[s.Track.ID]; ok {
			m.playingAlbum = a
		}
	}
	m.maybeSeek(s)
	return cmd
}

func (m *Model) setArtists(a []library.Artist) tea.Cmd {
	prev := m.selectedArtist()
	if m.wantArtist != "" {
		prev, m.wantArtist = m.wantArtist, ""
	}
	m.artists = a
	labels := make([]string, 0, len(a)+1)
	labels = append(labels, allArtists)
	want := 0
	for i, ar := range a {
		labels = append(labels, ar.Name)
		if ar.Name == prev {
			want = i + 1
		}
	}
	m.panes[paneArtists].setItemsKeep(labels, want)
	return m.artistChanged()
}

func (m *Model) setAlbums(artist string, a []library.Album) tea.Cmd {
	prev := m.selectedAlbumID()
	if m.allAlbumsSelected() {
		prev = sessionAllAlbums
	}
	if m.wantAlbum != "" {
		prev, m.wantAlbum = m.wantAlbum, ""
	}
	m.albumsFor, m.albums = artist, a
	want := 0 // "All albums"
	labels := []string{}
	if len(a) > 0 {
		labels = append(labels, allAlbums)
		labels = append(labels, albumLabels(a, artist == allArtists)...)
		for i, al := range a {
			if al.ID == prev {
				want = i + 1
			}
		}
	}
	m.panes[paneAlbums].setItemsKeep(labels, want)
	return m.albumChanged()
}

func (m *Model) selectedArtist() string {
	i := m.panes[paneArtists].selected()
	switch {
	case i == 0:
		return allArtists
	case i > 0 && i-1 < len(m.artists):
		return m.artists[i-1].Name
	}
	return ""
}

func (m *Model) albumsCurrent() bool { return m.albumsFor != "" && m.albumsFor == m.selectedArtist() }

func (m *Model) allAlbumsSelected() bool {
	return m.albumsCurrent() && len(m.albums) > 0 && m.panes[paneAlbums].selected() == 0
}

func (m *Model) selectedAlbum() (library.Album, bool) {
	if !m.albumsCurrent() {
		return library.Album{}, false
	}
	i := m.panes[paneAlbums].selected() - 1
	if i < 0 || i >= len(m.albums) {
		return library.Album{}, false
	}
	return m.albums[i], true
}

func (m *Model) selectedAlbumID() string {
	a, ok := m.selectedAlbum()
	if !ok {
		return ""
	}
	return a.ID
}

// tracksKey is what the tracks pane should show for the current selection:
// an album id, an artist key for "All albums", or "" for nothing.
func (m *Model) tracksKey() string {
	if m.allAlbumsSelected() {
		if m.albumsFor == allArtists {
			return "" // the whole library: too many albums to fetch
		}
		return artistKeyPrefix + m.albumsFor
	}
	return m.selectedAlbumID()
}

func (m *Model) artistChanged() tea.Cmd {
	artist := m.selectedArtist()
	if artist == "" {
		m.albums, m.albumsFor = nil, ""
		m.panes[paneAlbums].setItems(nil)
		return m.albumChanged()
	}
	if artist == m.albumsFor {
		return nil
	}
	m.albumsFor = "" // stale until albumsMsg arrives
	return m.loadAlbums(artist)
}

func (m *Model) albumChanged() tea.Cmd {
	key := m.tracksKey()
	m.tracksWant = key
	if key == m.tracksFor && key != "" {
		return nil
	}
	m.tracks, m.trackRows, m.tracksFor, m.tracksErr = nil, nil, "", nil
	m.panes[paneTracks].setItems(nil)
	if key == "" {
		return nil
	}
	return m.scheduleTracks(key)
}

// selectTrackRow puts the tracks cursor on the row of track id.
func (m *Model) selectTrackRow(id string) {
	for i, r := range m.trackRows {
		if r.kind == rowTrack && m.tracks[r.track].ID == id {
			m.panes[paneTracks].selectIndex(i)
			return
		}
	}
}

// cursorToPlaying puts the tracks cursor on the playing track if it is shown.
func (m *Model) cursorToPlaying() {
	if m.state.Track != nil {
		m.selectTrackRow(m.state.Track.ID)
	}
}

// --- playback -------------------------------------------------------------

func (m *Model) playTracks(tracks []library.Track, albums []library.Album, startID string) tea.Cmd {
	if m.deps.Player == nil {
		return m.flash("player not ready yet ("+m.playerStatus+")", true)
	}
	// Tracks Apple offers no stream for (not in this storefront's catalog)
	// are left out of the queue; starting on one starts at the next.
	playable := make([]library.Track, 0, len(tracks))
	for i, t := range tracks {
		if t.Playable {
			playable = append(playable, t)
		} else if t.ID == startID {
			startID = ""
			for _, n := range tracks[i+1:] {
				if n.Playable {
					startID = n.ID
					break
				}
			}
		}
	}
	if len(playable) == 0 {
		return m.flash("not available in your Apple Music storefront", true)
	}
	tracks = playable
	byID := make(map[string]library.Album, len(albums))
	for _, a := range albums {
		byID[a.ID] = a
	}
	ids := make([]string, len(tracks))
	start := 0
	m.queueAlbums = make(map[string]library.Album, len(tracks))
	for i, t := range tracks {
		ids[i] = t.ID
		m.queueAlbums[t.ID] = byID[t.AlbumID]
		if t.ID == startID {
			start = i
		}
	}
	m.playingAlbum = byID[tracks[start].AlbumID]
	if err := m.deps.Player.PlayTracks(ids, start); err != nil {
		return m.flash(err.Error(), true)
	}
	return nil
}

// playSelection plays what is under the cursor. In Albums: the album (or all
// of the artist's albums). In Tracks: the listed tracks from the selected
// row on (a header row starts at its album's first track).
func (m *Model) playSelection() tea.Cmd {
	key := m.tracksKey()
	if key == "" {
		if m.allAlbumsSelected() {
			return m.flash("pick an artist to play all of its albums", true)
		}
		return nil
	}
	startID := ""
	if m.focus == paneTracks && m.tracksFor == key {
		if i := m.panes[paneTracks].selected(); i >= 0 && i < len(m.trackRows) {
			r := m.trackRows[i]
			switch r.kind {
			case rowTrack:
				startID = m.tracks[r.track].ID
			case rowHeader:
				for _, t := range m.tracks {
					if t.AlbumID == r.album.ID {
						startID = t.ID
						break
					}
				}
			}
		}
	}
	if m.tracksFor == key && m.tracksErr == nil {
		return m.playTracks(m.tracks, m.albumsOfTracks(), startID)
	}
	m.pendingPlay = &pendingPlay{key: key, trackID: startID}
	if m.tracksLoading == key {
		return nil
	}
	m.tracksLoading = key
	return m.loadTracks(key)
}

// albumsOfTracks returns the albums the current tracks rows refer to.
func (m *Model) albumsOfTracks() []library.Album {
	seen := map[string]bool{}
	var out []library.Album
	for _, r := range m.trackRows {
		if r.kind != rowAll && !seen[r.album.ID] {
			seen[r.album.ID] = true
			out = append(out, r.album)
		}
	}
	return out
}

func (m *Model) withPlayer(f func(player.Player) error) tea.Cmd {
	if m.deps.Player == nil {
		return m.flash("player not ready yet", true)
	}
	if err := f(m.deps.Player); err != nil {
		return m.flash(err.Error(), true)
	}
	return nil
}

func (m *Model) seekBy(d time.Duration) tea.Cmd {
	if m.state.Track == nil {
		return nil
	}
	pos := m.state.Position + d
	if pos < 0 {
		pos = 0
	}
	m.state.Position = pos
	return m.withPlayer(func(p player.Player) error { return p.Seek(pos) })
}

func (m *Model) volumeBy(d float64) tea.Cmd {
	m.volume = min(1, max(0, m.volume+d))
	v := m.volume
	return m.withPlayer(func(p player.Player) error { return p.SetVolume(v) })
}

// --- keys -----------------------------------------------------------------

// keyName is the name keys are matched by. String() returns the key's text
// when it has any, so a terminal that sends Tab with Text "\t" would yield
// "\t" rather than "tab"; control characters fall back to the keystroke.
func keyName(msg tea.KeyPressMsg) string {
	if t := msg.Text; t != "" && strings.IndexFunc(t, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return msg.Keystroke()
	}
	return msg.String()
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	k := keyName(msg)
	if k == "ctrl+c" {
		return tea.Quit
	}
	if m.themes != nil {
		return m.handleThemeKey(k)
	}
	if c := m.confirm; c != nil {
		m.confirm = nil
		if k == "y" || k == "Y" {
			return c.yes()
		}
		return m.flash("cancelled", false)
	}
	if m.filtering {
		return m.handleFilterKey(msg, k)
	}
	if m.showHelp {
		m.showHelp = false
		if k == "?" || k == "esc" || k == "q" {
			return nil
		}
	}
	if m.mode == modeSearch {
		return m.handleSearchKey(msg, k)
	}
	if cmd, ok := m.playbackKey(k); ok {
		return cmd
	}
	p := m.panes[m.focus]
	switch k {
	case "s":
		return m.openSearch()
	case "D":
		a, ok := m.libraryDeleteTarget()
		if !ok {
			return m.flash("select an album to remove", false)
		}
		return m.confirmDelete(a)
	case "tab", "l":
		m.focus = (m.focus + 1) % numPanes
		return nil
	case "shift+tab", "h":
		m.focus = (m.focus + numPanes - 1) % numPanes
		return nil
	case "1", "2", "3":
		m.focus = int(k[0] - '1')
		return nil
	case "down", "j":
		p.move(1)
	case "up", "k":
		p.move(-1)
	case "pgdown", "ctrl+d":
		p.move(m.listHeight() / 2)
	case "pgup", "ctrl+u":
		p.move(-m.listHeight() / 2)
	case "home", "g":
		p.home()
	case "end", "G":
		p.end()
	case "/":
		m.filtering = true
		return nil
	case "esc":
		if p.filter != "" {
			p.setFilter("")
		}
	case "enter":
		return m.enter()
	case "R":
		if m.deps.Src == nil {
			return m.flash("offline: cannot sync", true)
		}
		if m.syncing {
			return nil
		}
		m.syncing = true
		return m.syncCmd()
	default:
		return nil
	}
	return m.selectionMoved()
}

// enter: on an artist, move to Albums; on an album, play it and move to
// Tracks with the cursor on the first track; on a track, play from there.
func (m *Model) enter() tea.Cmd {
	switch m.focus {
	case paneArtists:
		m.focus = paneAlbums
		return nil
	case paneAlbums:
		cmd := m.playSelection()
		m.focus = paneTracks
		if m.tracksFor == m.tracksKey() && len(m.tracks) > 0 {
			m.selectTrackRow(m.tracks[0].ID)
		} // otherwise onTracks puts the cursor on the start track
		return cmd
	}
	return m.playSelection()
}

// playbackKey handles the keys shared by the library and search views.
func (m *Model) playbackKey(k string) (tea.Cmd, bool) {
	switch k {
	case "q":
		return tea.Quit, true
	case "?":
		m.showHelp = true
		return nil, true
	case "space":
		if m.state.Track == nil && m.resume != nil {
			return m.resumePlayback(), true
		}
		return m.withPlayer(func(p player.Player) error { return p.Toggle() }), true
	case "n", ">":
		return m.withPlayer(func(p player.Player) error { return p.Next() }), true
	case "p", "<":
		return m.withPlayer(func(p player.Player) error { return p.Previous() }), true
	case "x":
		m.playingAlbum = library.Album{}
		return m.withPlayer(func(p player.Player) error { return p.Stop() }), true
	case "right", ".":
		return m.seekBy(10 * time.Second), true
	case "left", ",":
		return m.seekBy(-10 * time.Second), true
	case "shift+right":
		return m.seekBy(60 * time.Second), true
	case "shift+left":
		return m.seekBy(-60 * time.Second), true
	case "+", "=":
		return m.volumeBy(0.05), true
	case "-", "_":
		return m.volumeBy(-0.05), true
	case "o":
		return m.jumpToPlaying(), true
	case "T":
		m.openThemePicker()
		return nil, true
	}
	return nil, false
}

func (m *Model) selectionMoved() tea.Cmd {
	switch m.focus {
	case paneArtists:
		return m.artistChanged()
	case paneAlbums:
		return m.albumChanged()
	}
	return nil
}

func (m *Model) handleFilterKey(msg tea.KeyPressMsg, k string) tea.Cmd {
	p := m.panes[m.focus]
	switch k {
	case "esc":
		m.filtering = false
		p.setFilter("")
	case "tab", "shift+tab":
		// Keep the filter and move on, as outside filter mode.
		m.filtering = false
		cmd := m.selectionMoved()
		if k == "tab" {
			m.focus = (m.focus + 1) % numPanes
		} else {
			m.focus = (m.focus + numPanes - 1) % numPanes
		}
		return cmd
	case "enter":
		m.filtering = false
		cmd := m.selectionMoved()
		return tea.Batch(cmd, m.enter())
	case "backspace":
		if r := []rune(p.filter); len(r) > 0 {
			p.setFilter(string(r[:len(r)-1]))
		}
	case "down", "ctrl+n":
		p.move(1)
	case "up", "ctrl+p":
		p.move(-1)
	default:
		t := msg.Text
		if t == "" && k == "space" {
			t = " "
		}
		if t == "" || strings.IndexFunc(t, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return nil // never type control characters (tab, esc, …) into a filter
		}
		p.setFilter(p.filter + t)
	}
	return m.selectionMoved()
}

// jumpToPlaying selects the playing album's artist, album and track.
func (m *Model) jumpToPlaying() tea.Cmd {
	a := m.playingAlbum
	if a.ID == "" {
		return nil
	}
	if a.Source == SourceCatalog {
		// Played from search: show it there if it is still listed.
		if s := m.search; s != nil {
			m.mode = modeSearch
			s.editing = false
			for i, al := range s.albums {
				if al.ID == a.ID {
					s.panes[paneAlbums].selectIndex(i)
					s.focus = paneTracks
					if m.state.Track != nil && s.tracksFor == a.ID {
						selectRow(s.panes[paneTracks], s.trackRows, s.tracks, m.state.Track.ID)
					}
					return m.searchAlbumChanged()
				}
			}
		}
		return m.flash("playing “"+a.Title+"” from search results", false)
	}
	if m.state.Track != nil {
		m.wantTrack = m.state.Track.ID
	}
	ap := m.panes[paneArtists]
	ap.setFilter("")
	for i, ar := range m.artists {
		if library.ArtistKey(ar.Name) == library.ArtistKey(a.Artist) {
			ap.selectIndex(i + 1)
			break
		}
	}
	m.panes[paneAlbums].setFilter("")
	m.panes[paneTracks].setFilter("")
	m.focus = paneTracks
	artist := m.selectedArtist()
	if artist == m.albumsFor {
		for i, al := range m.albums {
			if al.ID == a.ID {
				m.panes[paneAlbums].selectIndex(i + 1)
			}
		}
		if cmd := m.albumChanged(); cmd != nil {
			return cmd
		}
		m.selectTrackRow(m.wantTrack) // tracks already shown
		m.wantTrack = ""
		return nil
	}
	// Albums arrive asynchronously; setAlbums will select wantAlbum.
	m.wantAlbum = a.ID
	return m.loadAlbums(artist)
}
