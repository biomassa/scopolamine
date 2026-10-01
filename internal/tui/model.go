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

	// ScanLocal scans the local library folder (nil: no local mode).
	ScanLocal func(ctx context.Context, progress func(done, total int)) (changed int, err error)
	// LocalRoot is the local music folder, or "" if it does not exist.
	LocalRoot string
	// DefaultLocalRoot is the folder for an empty input in the folder box.
	DefaultLocalRoot string
	// SetLocalRoot makes path the local music folder and saves it (nil: the
	// folder cannot change).
	SetLocalRoot func(path string) error
	// StartApple starts the Apple Music player when the Apple view shows and
	// the player does not run (nil: nothing to start). It returns at once;
	// a PlayerReadyMsg or PlayerFailedMsg follows.
	StartApple func()
	// StopApple shuts the Apple Music player down after appleIdleAfter in
	// the local view (nil: it never shuts down). It returns when the player
	// has shut down.
	StopApple func()
	// FadeAway fades the music out before the quit and returns when the
	// fade is over (nil: quit at once).
	FadeAway func()
}

// Messages sent in from outside (see cmd/scopolamine).
type (
	// PlayerStatusMsg is progress text while the player starts.
	PlayerStatusMsg string
	// PlayerReadyMsg hands over a started player.
	PlayerReadyMsg struct{ Player player.Player }
	// PlayerFailedMsg reports that playback could not start.
	PlayerFailedMsg struct{ Err error }
	// AppleFailedMsg reports that the Apple Music player could not start.
	// The next switch to the Apple Music mode tries again.
	AppleFailedMsg struct{ Err error }
	// LocalChangedMsg reports changes in the local library folder.
	LocalChangedMsg struct{}
)

type (
	artistsMsg struct {
		src     string
		artists []library.Artist
		err     error
	}
	albumsMsg struct {
		src    string
		artist string
		albums []library.Album
		err    error
	}
	tracksMsg struct {
		src    string
		key    string
		tracks []library.Track
		albums []library.Album
		err    error
	}
	syncDoneMsg struct {
		src string
		v   *libView // the view that started the sync
		n   int
		err error
	}
	syncProgressMsg struct {
		src      string
		v        *libView
		n, total int
	}
	stateMsg struct {
		s  player.State
		ok bool
	}
	clearNoticeMsg struct{ seq int }
)

// libView is the browsing state of one library mode (Apple Music or
// local). Model embeds the view in use, so m.artists and the like refer to
// it; a mode switch swaps the pointer, and each mode keeps its state.
type libView struct {
	source  string // library.SourceApple or library.SourceLocal
	folders bool   // local only: folder sorting instead of metadata
	loaded  bool   // the first load has started
	// jumpOnInfo: jump to the resume point when its lookup is done.
	jumpOnInfo bool

	focus int
	panes [numPanes]*pane

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

	resume *resumePoint // the last track and position of this mode

	syncing       bool
	syncCh        chan tea.Msg
	syncN, syncOf int
}

func newLibView(source string) *libView {
	v := &libView{source: source}
	v.panes[paneArtists] = &pane{title: "Artists"}
	v.panes[paneAlbums] = &pane{title: "Albums"}
	v.panes[paneTracks] = &pane{title: "Tracks"}
	return v
}

// Model is the Bubble Tea model.
type Model struct {
	*libView // the mode in use

	apple, local *libView

	deps Deps
	ctx  context.Context

	width, height int
	filtering     bool

	pendingSeek *seekTarget

	// queueAlbums maps queued track ids to their albums, so "what is
	// playing" is known even when the queue spans several albums.
	queueAlbums  map[string]library.Album
	playingAlbum library.Album
	state        player.State
	stateCh      tea.Cmd
	volume       float64
	playerStatus string

	notice    string
	noticeErr bool
	noticeSeq int
	showHelp  bool

	mode   int // modeLibrary or modeSearch
	search *searchState

	confirm  *confirmation
	removing map[string]bool // library album ids being removed

	cover        *coverView
	coverLoading map[string]bool

	themes *themePicker // the open theme picker, or nil

	appleState   int  // appleOff, appleStarting, appleReady, or appleStopping
	appleIdleSeq int  // the number of the idle timer that counts
	appleIdleDue bool // the idle time is over; stop when no Apple Music plays
	appleFailed  bool // the last start failed; the next switch tries again

	quitting bool // q: the music fades out, then the program quits

	localRoot string        // the local music folder, or ""
	folder    *folderPrompt // the open folder box, or nil
}

type pendingPlay struct {
	key     string // tracks key
	trackID string // "" = from the first track
}

// New builds the model.
func New(ctx context.Context, d Deps) *Model {
	m := &Model{deps: d, ctx: ctx, volume: d.Volume, localRoot: d.LocalRoot}
	if m.volume <= 0 {
		m.volume = 1
	}
	m.apple, m.local = newLibView(library.SourceApple), newLibView(library.SourceLocal)
	m.libView = m.apple
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
	cmds := []tea.Cmd{m.showView(m.libView), m.jumpToCurrent()}
	// Both libraries refresh in the background at the start, whichever
	// mode shows.
	if m.deps.AutoSync && m.deps.Src != nil {
		cmds = append(cmds, m.inView(m.apple, m.startSync))
	}
	if m.localAvailable() {
		cmds = append(cmds, m.inView(m.local, m.startSync))
	}
	if m.deps.Player != nil {
		cmds = append(cmds, m.watchPlayer())
	}
	return tea.Batch(cmds...)
}

// view returns the view of source.
func (m *Model) view(source string) *libView {
	if source == library.SourceLocal {
		return m.local
	}
	return m.apple
}

// inView runs f with v as the view in use and returns to the previous
// view. Messages for a mode apply to that mode's view, also when the user
// has switched modes in the meantime.
func (m *Model) inView(v *libView, f func() tea.Cmd) tea.Cmd {
	prev := m.libView
	m.libView = v
	defer func() { m.libView = prev }()
	return f()
}

// showView makes v the view in use and loads it the first time.
func (m *Model) showView(v *libView) tea.Cmd {
	m.libView = v
	idle := m.appleOnShow(v)
	if v.loaded {
		return idle
	}
	v.loaded = true
	return tea.Batch(idle, m.loadArtists(), m.loadResumeInfo())
}

// startSync syncs the view's library: the Apple Music album list, or a
// scan of the local folder.
func (m *Model) startSync() tea.Cmd {
	if m.syncing {
		return nil
	}
	if m.source == library.SourceLocal {
		if !m.localAvailable() {
			return nil
		}
	} else if m.deps.Src == nil {
		return m.flash("offline: cannot sync", true)
	}
	m.syncing = true
	return m.syncCmd()
}

// --- commands -------------------------------------------------------------

func (m *Model) loadArtists() tea.Cmd {
	store, src, folders := m.deps.Store, m.source, m.folders
	return func() tea.Msg {
		var (
			a   []library.Artist
			err error
		)
		if folders {
			a, err = store.FolderArtists(m.ctx)
		} else {
			a, err = store.Artists(m.ctx, src)
		}
		return artistsMsg{src, a, err}
	}
}

func (m *Model) loadAlbums(artist string) tea.Cmd {
	store, src, folders := m.deps.Store, m.source, m.folders
	return func() tea.Msg {
		var (
			a   []library.Album
			err error
		)
		switch {
		case folders && artist == allArtists:
			a, err = store.FolderAlbums(m.ctx, "")
		case folders:
			a, err = store.FolderAlbums(m.ctx, artist)
		case artist == allArtists:
			a, err = store.AllAlbums(m.ctx, src)
		default:
			a, err = store.AlbumsByArtist(m.ctx, src, artist)
		}
		return albumsMsg{src, artist, a, err}
	}
}

// syncCmd runs the album sync in the background. Progress and the final
// result arrive through one channel, read one message per command.
func (m *Model) syncCmd() tea.Cmd {
	store, apple, scan, source, v := m.deps.Store, m.deps.Src, m.deps.ScanLocal, m.source, m.libView
	ch := make(chan tea.Msg, 1)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Minute)
		defer cancel()
		progress := func(n, total int) {
			select { // drop intermediate updates the UI hasn't read yet
			case ch <- syncProgressMsg{source, v, n, total}:
			default:
			}
		}
		var (
			n   int
			err error
		)
		if source == library.SourceLocal {
			n, err = scan(ctx, progress)
		} else {
			n, err = library.SyncAppleAlbums(ctx, store, apple, progress)
		}
		ch <- syncDoneMsg{source, v, n, err}
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
		return m, m.inView(m.view(msg.src), func() tea.Cmd {
			if msg.err != nil {
				return m.flash("library: "+msg.err.Error(), true)
			}
			return m.setArtists(msg.artists)
		})

	case albumsMsg:
		return m, m.inView(m.view(msg.src), func() tea.Cmd {
			if msg.err != nil {
				return m.flash("library: "+msg.err.Error(), true)
			}
			if msg.artist != m.selectedArtist() {
				return nil // stale
			}
			return m.setAlbums(msg.artist, msg.albums)
		})

	case tracksLoadMsg:
		return m, m.inView(m.view(msg.src), func() tea.Cmd {
			if msg.key != m.tracksWant || msg.key == m.tracksFor || msg.key == m.tracksLoading {
				return nil // cursor moved on, or already there
			}
			m.tracksLoading = msg.key
			return m.loadTracks(msg.key)
		})

	case tracksMsg:
		return m, m.inView(m.view(msg.src), func() tea.Cmd { return m.onTracks(msg) })

	case syncProgressMsg:
		if msg.v != m.view(msg.src) {
			return m, nil // a scan of an old local folder
		}
		return m, m.inView(m.view(msg.src), func() tea.Cmd {
			m.syncN, m.syncOf = msg.n, msg.total
			return m.readSync()
		})

	case syncDoneMsg:
		if msg.v != m.view(msg.src) {
			return m, m.inView(m.view(msg.src), m.loadArtists) // a scan of an old local folder
		}
		return m, m.inView(m.view(msg.src), func() tea.Cmd {
			m.syncing = false
			m.syncN, m.syncOf = 0, 0
			if msg.err != nil {
				return m.flash("sync failed: "+msg.err.Error(), true)
			}
			m.albumsFor, m.tracksFor = "", "" // re-read the current selection
			if m.source == library.SourceLocal {
				if msg.n == 0 {
					return m.loadArtists()
				}
				return tea.Batch(m.loadArtists(), m.flash(fmt.Sprintf("local library: %d files read", msg.n), false))
			}
			return tea.Batch(m.loadArtists(), m.flash(fmt.Sprintf("library synced: %d albums", msg.n), false))
		})

	case LocalChangedMsg:
		return m, m.inView(m.local, m.startSync)

	case PlayerStatusMsg:
		m.playerStatus = string(msg)
		return m, nil

	case PlayerReadyMsg:
		m.playerStatus = ""
		m.appleState = appleReady
		idle := m.armAppleIdle() // the local view shows already
		if m.source == library.SourceApple {
			idle = nil
		}
		if msg.Player != nil && m.deps.Player == nil {
			m.deps.Player = msg.Player
			_ = msg.Player.SetVolume(m.volume)
			return m, tea.Batch(idle, m.watchPlayer())
		}
		return m, idle

	case appleIdleMsg:
		if msg.seq != m.appleIdleSeq {
			return m, nil // the Apple view showed in the meantime
		}
		m.appleIdleDue = true
		return m, m.maybeStopApple()

	case appleStoppedMsg:
		return m, m.onAppleStopped()

	case PlayerFailedMsg:
		m.playerStatus = "playback unavailable"
		return m, m.flash("player: "+msg.Err.Error(), true)

	case AppleFailedMsg:
		return m, m.onAppleFailed(msg.Err)

	case stateMsg:
		if !msg.ok {
			m.deps.Player = nil
			m.playerStatus = "player stopped"
			return m, nil
		}
		return m, tea.Batch(m.applyState(msg.s), m.maybeStopApple(), m.stateCh)

	case localRootSetMsg:
		return m, m.onLocalRootSet(msg)

	case resumeInfoMsg:
		v := m.view(msg.src)
		m.inView(v, func() tea.Cmd { m.onResumeInfo(msg); return nil })
		if v == m.libView && v.jumpOnInfo {
			return m, m.jumpToCurrent()
		}
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
		// Only a track of the queue that scopolamine started counts. During
		// a fade-out, the old track still plays: its mode keeps its resume
		// point.
		if a, ok := m.queueAlbums[s.Track.ID]; ok {
			m.playingAlbum = a
			if pv := m.playingView(); pv != nil {
				pv.resume = nil // this mode plays; its old resume point is moot
			}
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
	// The other mode's music stops: it keeps its track and position as its
	// resume point.
	if pv := m.playingView(); pv != nil && pv != m.libView {
		m.keepResume(pv)
	}
	// enter on this mode's resume track continues at the saved position.
	if r := m.resume; r != nil && ids[start] == r.trackID && r.pos > 2*time.Second && m.playingView() != m.libView {
		m.pendingSeek = &seekTarget{trackID: r.trackID, pos: r.pos}
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
	// A row that holds the current track of this mode continues it: nothing
	// if it plays, play if it is paused, the saved position of a resume
	// point.
	if id, albumID, kind := m.currentTrack(); id != "" && m.rowHolds(key, id, albumID) {
		switch kind {
		case curPlaying:
			return nil
		case curPaused:
			return m.withPlayer(player.Player.Play)
		case curResume:
			startID = id
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

// findTrack finds track id in the tracks that v shows.
func (m *Model) findTrack(v *libView, id string) (library.Track, bool) {
	for _, t := range v.tracks {
		if t.ID == id {
			return t, true
		}
	}
	return library.Track{}, false
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
	if m.quitting {
		return nil // only ctrl+c stops the fade
	}
	if m.folder != nil {
		return m.handleFolderKey(msg, k)
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
	local := m.source == library.SourceLocal
	switch k {
	case "L":
		return m.switchMode()
	case "v":
		if !local {
			return nil
		}
		return m.toggleFolders()
	case "F":
		if !local || m.deps.SetLocalRoot == nil {
			return nil
		}
		m.openFolderPrompt()
		return nil
	case "s":
		if local {
			return nil // no search in local mode
		}
		return m.openSearch()
	case "D":
		if local {
			return nil // local files are not removed
		}
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
		return m.startSync()
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
		// The cursor goes to the current track if the album holds it, else
		// to the first track.
		target := ""
		if id, albumID, _ := m.currentTrack(); id != "" && m.rowHolds(m.tracksKey(), id, albumID) {
			target = id
		}
		cmd := m.playSelection()
		m.focus = paneTracks
		if m.tracksFor == m.tracksKey() && len(m.tracks) > 0 {
			if target == "" {
				target = m.tracks[0].ID
			}
			m.selectTrackRow(target)
		} else if target != "" {
			m.wantTrack = target
		} // otherwise onTracks puts the cursor on the start track
		return cmd
	}
	return m.playSelection()
}

// playbackKey handles the keys shared by the library and search views.
func (m *Model) playbackKey(k string) (tea.Cmd, bool) {
	switch k {
	case "q":
		return m.quit(), true
	case "?":
		m.showHelp = true
		return nil, true
	case "space":
		return m.space(), true
	case "]", "n", ">":
		return m.withPlayer(func(p player.Player) error { return p.Next() }), true
	case "[", "p", "<":
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

// switchMode switches between Apple Music and the local library. Each mode
// keeps its own selection; the music plays on.
func (m *Model) switchMode() tea.Cmd {
	if m.source == library.SourceApple {
		if !m.localAvailable() {
			if m.deps.SetLocalRoot != nil {
				m.openFolderPrompt() // no folder yet: ask for one
				return nil
			}
			return m.flash("no local library (set local_root in the config)", true)
		}
		m.filtering = false
		return tea.Batch(m.showView(m.local), m.jumpToCurrent())
	}
	m.filtering = false
	return tea.Batch(m.showView(m.apple), m.jumpToCurrent())
}

// jumpToCurrent puts the cursor on the current track of the mode that
// shows: the track that plays or is paused in this mode, else the resume
// point of the mode.
func (m *Model) jumpToCurrent() tea.Cmd {
	m.jumpOnInfo = false
	if a := m.playingAlbum; m.state.Track != nil && a.ID != "" && a.Source != SourceCatalog && m.view(a.Source) == m.libView {
		return m.jumpToAlbum(a, m.state.Track.ID)
	}
	r := m.resume
	if r == nil {
		return nil
	}
	if r.album.ID == "" {
		m.jumpOnInfo = true // the lookup of the resume point is not done yet
		return nil
	}
	return m.jumpToAlbum(r.album, r.trackID)
}

// sortName is the local sorting: "metadata" or "folders".
func (m *Model) sortName() string {
	if m.folders {
		return "folders"
	}
	return "metadata"
}

// toggleFolders switches the local view between metadata and folders.
func (m *Model) toggleFolders() tea.Cmd {
	v := m.libView
	v.folders = !v.folders
	for _, p := range v.panes {
		p.filter = ""
	}
	v.artists, v.albums, v.tracks, v.trackRows = nil, nil, nil, nil
	v.albumsFor, v.tracksFor, v.tracksWant, v.tracksLoading = "", "", "", ""
	for _, p := range v.panes {
		p.setItems(nil)
	}
	return m.loadArtists()
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
	if want := m.view(a.Source); a.Source != SourceCatalog && want != m.libView {
		// The playing album is in the other mode: switch to it first.
		cmd := m.showView(want)
		folders := strings.HasPrefix(a.ID, library.FolderPrefix)
		if want.source == library.SourceLocal && want.folders != folders {
			cmd = tea.Batch(cmd, m.toggleFolders())
		}
		return tea.Batch(cmd, m.jumpToPlaying())
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
	trackID := ""
	if m.state.Track != nil {
		trackID = m.state.Track.ID
	}
	return m.jumpToAlbum(a, trackID)
}

// jumpToAlbum selects album a of the view that shows, with the cursor on
// track trackID in the track column.
func (m *Model) jumpToAlbum(a library.Album, trackID string) tea.Cmd {
	m.wantTrack = trackID
	ap := m.panes[paneArtists]
	ap.setFilter("")
	found := false
	for i, ar := range m.artists {
		if library.ArtistKey(ar.Name) == library.ArtistKey(a.Artist) {
			ap.selectIndex(i + 1)
			found = true
			break
		}
	}
	if !found {
		// The lists of this view are not loaded yet (a mode or sort
		// switch): select everything when they arrive.
		m.wantArtist, m.wantAlbum = a.Artist, a.ID
		m.focus = paneTracks
		return nil
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

// quit fades the music out if it plays, and then quits.
func (m *Model) quit() tea.Cmd {
	fade := m.deps.FadeAway
	if fade == nil || !m.state.Playing {
		return tea.Quit
	}
	m.quitting = true
	return func() tea.Msg {
		fade()
		return tea.Quit()
	}
}
