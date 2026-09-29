package tui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/applemusic"
	"github.com/biomassa/scopolamine/internal/library"
)

// Catalog is what the search view needs from Apple Music.
type Catalog interface {
	Search(ctx context.Context, term string) (applemusic.SearchResult, error)
	ArtistAlbums(ctx context.Context, artistID string) ([]applemusic.CatalogAlbum, error)
	CatalogAlbumTracks(ctx context.Context, albumID string) ([]applemusic.Track, error)
	AddAlbumAndWait(ctx context.Context, catalogID string) (applemusic.Album, error)
	DeleteLibraryAlbum(ctx context.Context, libraryID string) error
}

const (
	modeLibrary = iota
	modeSearch
)

// SourceCatalog marks albums from search results (not in the library cache).
const SourceCatalog = "catalog"

const matchingAlbums = "Matching albums"

// searchState is the Apple Music search view: the same three columns, fed
// from the catalog. Artists row 0 is the albums matching the query; an
// artist row lists that artist's discography.
type searchState struct {
	query   string
	editing bool
	ran     string // query the results belong to
	running bool
	err     error
	seq     int // debounce generation

	focus int
	panes [numPanes]*pane

	artists []applemusic.CatalogArtist
	results []applemusic.CatalogAlbum // albums matching the query

	albums        []applemusic.CatalogAlbum // shown in the Albums column
	albumsFor     string                    // "" = results, else artist id
	albumsWant    string
	albumsLoading bool
	albumsErr     error

	tracks        []library.Track
	trackRows     []trackRow
	tracksFor     string // catalog album id
	tracksWant    string
	tracksLoading string
	tracksErr     error
	pendingPlay   *pendingPlay

	adding map[string]bool // catalog album ids being added
}

type (
	searchDebounceMsg struct{ seq int }
	searchResultMsg   struct {
		query string
		res   applemusic.SearchResult
		err   error
	}
	searchAlbumsLoadMsg struct{ artistID string }
	searchAlbumsMsg     struct {
		artistID string
		albums   []applemusic.CatalogAlbum
		err      error
	}
	searchTracksLoadMsg struct{ albumID string }
	searchTracksMsg     struct {
		albumID string
		tracks  []library.Track
		err     error
	}
	addedMsg struct {
		catalogID string
		album     library.Album
		err       error
	}
)

const searchDebounce = 400 * time.Millisecond

func newSearchState() *searchState {
	s := &searchState{adding: map[string]bool{}}
	s.panes[paneArtists] = &pane{title: "Artists"}
	s.panes[paneAlbums] = &pane{title: "Albums"}
	s.panes[paneTracks] = &pane{title: "Tracks"}
	return s
}

// catalogAlbum converts a catalog album for playback/markers.
func catalogAlbum(a applemusic.CatalogAlbum) library.Album {
	return library.Album{
		ID: a.ID, Source: SourceCatalog, CatalogID: a.ID, Title: a.Title, Artist: a.Artist,
		Year: library.YearOf(a.ReleaseDate), ReleaseDate: a.ReleaseDate, TrackCount: a.TrackCount,
		Genre: a.Genre, ArtworkURL: a.ArtworkURL,
	}
}

func (m *Model) openSearch() tea.Cmd {
	if m.deps.Catalog == nil {
		return m.flash("search needs Apple Music (not available offline)", true)
	}
	if m.search == nil {
		m.search = newSearchState()
	}
	m.mode = modeSearch
	m.search.editing = true
	return nil
}

func (m *Model) closeSearch() { m.mode = modeLibrary }

// --- selection --------------------------------------------------------------

func (s *searchState) selectedArtistID() (string, bool) {
	i := s.panes[paneArtists].selected()
	switch {
	case i == 0:
		return "", true // matching albums
	case i > 0 && i-1 < len(s.artists):
		return s.artists[i-1].ID, true
	}
	return "", false
}

func (s *searchState) selectedAlbum() (applemusic.CatalogAlbum, bool) {
	if s.albumsFor != s.albumsWant {
		return applemusic.CatalogAlbum{}, false
	}
	i := s.panes[paneAlbums].selected()
	if i < 0 || i >= len(s.albums) {
		return applemusic.CatalogAlbum{}, false
	}
	return s.albums[i], true
}

// shownAlbum is the album whose tracks are listed.
func (s *searchState) shownAlbum() (applemusic.CatalogAlbum, bool) {
	for _, a := range s.albums {
		if a.ID == s.tracksFor {
			return a, true
		}
	}
	return applemusic.CatalogAlbum{}, false
}

func searchAlbumLabels(albums []applemusic.CatalogAlbum, withArtist bool) []string {
	out := make([]string, len(albums))
	for i, a := range albums {
		l := a.Title
		if withArtist {
			l = a.Artist + " — " + a.Title
		}
		year := "····"
		if y := library.YearOf(a.ReleaseDate); y > 0 {
			year = strconv.Itoa(y)
		}
		out[i] = year + "  " + l
	}
	return out
}

func (m *Model) searchArtistChanged() tea.Cmd {
	s := m.search
	id, ok := s.selectedArtistID()
	if !ok {
		return nil
	}
	if id == s.albumsWant && (id == "" || s.albumsFor == id || s.albumsLoading) {
		return nil
	}
	s.albumsWant = id
	if id == "" {
		m.setSearchAlbums("", s.results)
		return m.searchAlbumChanged()
	}
	s.albums, s.albumsErr = nil, nil
	s.panes[paneAlbums].setItems(nil)
	m.clearSearchTracks()
	return tea.Tick(tracksDebounce, func(time.Time) tea.Msg { return searchAlbumsLoadMsg{id} })
}

func (m *Model) setSearchAlbums(artistID string, albums []applemusic.CatalogAlbum) {
	s := m.search
	s.albumsFor, s.albums = artistID, albums
	s.panes[paneAlbums].setItems(searchAlbumLabels(albums, artistID == ""))
}

func (m *Model) clearSearchTracks() {
	s := m.search
	s.tracks, s.trackRows, s.tracksFor, s.tracksWant, s.tracksErr = nil, nil, "", "", nil
	s.panes[paneTracks].setItems(nil)
}

func (m *Model) searchAlbumChanged() tea.Cmd {
	s := m.search
	a, ok := s.selectedAlbum()
	if !ok {
		m.clearSearchTracks()
		return nil
	}
	if a.ID == s.tracksFor {
		return nil
	}
	m.clearSearchTracks()
	s.tracksWant = a.ID
	return tea.Tick(tracksDebounce, func(time.Time) tea.Msg { return searchTracksLoadMsg{a.ID} })
}

// --- commands ---------------------------------------------------------------

func (m *Model) runSearch() tea.Cmd {
	s := m.search
	q := strings.TrimSpace(s.query)
	if q == "" || q == s.ran {
		return nil
	}
	s.running, s.err = true, nil
	cat := m.deps.Catalog
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 20*time.Second)
		defer cancel()
		res, err := cat.Search(ctx, q)
		return searchResultMsg{q, res, err}
	}
}

func (m *Model) loadSearchAlbums(artistID string) tea.Cmd {
	cat := m.deps.Catalog
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		a, err := cat.ArtistAlbums(ctx, artistID)
		return searchAlbumsMsg{artistID, a, err}
	}
}

func (m *Model) loadSearchTracks(albumID string) tea.Cmd {
	cat := m.deps.Catalog
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		remote, err := cat.CatalogAlbumTracks(ctx, albumID)
		tracks := make([]library.Track, 0, len(remote))
		for _, r := range remote {
			tracks = append(tracks, library.Track{
				ID: r.ID, AlbumID: albumID, CatalogID: r.CatalogID, Title: r.Title, Artist: r.Artist,
				Disc: r.Disc, Number: r.Number, Duration: r.Duration, Playable: r.Playable,
			})
		}
		return searchTracksMsg{albumID, tracks, err}
	}
}

// addSelected adds the selected (or shown) album to the library.
func (m *Model) addSelected() tea.Cmd {
	s := m.search
	a, ok := s.selectedAlbum()
	if s.focus == paneTracks || !ok {
		a, ok = s.shownAlbum()
	}
	if !ok {
		return nil
	}
	if a.LibraryID != "" {
		return m.flash("“"+a.Title+"” is already in your library", false)
	}
	if s.adding[a.ID] {
		return nil
	}
	s.adding[a.ID] = true
	cat, store := m.deps.Catalog, m.deps.Store
	return tea.Batch(m.flash("adding “"+a.Title+"” to your library…", false), func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, time.Minute)
		defer cancel()
		lib, err := cat.AddAlbumAndWait(ctx, a.ID)
		if err != nil {
			return addedMsg{catalogID: a.ID, err: err}
		}
		la := library.FromApple(lib)
		if err := store.UpsertAlbum(ctx, library.SourceApple, la); err != nil {
			return addedMsg{catalogID: a.ID, err: err}
		}
		return addedMsg{catalogID: a.ID, album: la}
	})
}

func (m *Model) playSearchSelection() tea.Cmd {
	s := m.search
	var a applemusic.CatalogAlbum
	var ok bool
	if s.focus == paneTracks {
		a, ok = s.shownAlbum()
	} else {
		a, ok = s.selectedAlbum()
	}
	if !ok {
		return nil
	}
	startID := ""
	if s.focus == paneTracks {
		if i := s.panes[paneTracks].selected(); i >= 0 && i < len(s.trackRows) && s.trackRows[i].kind == rowTrack {
			startID = s.tracks[s.trackRows[i].track].ID
		}
	}
	if s.tracksFor == a.ID && s.tracksErr == nil {
		return m.playTracks(s.tracks, []library.Album{catalogAlbum(a)}, startID)
	}
	s.pendingPlay = &pendingPlay{key: a.ID, trackID: startID}
	if s.tracksLoading == a.ID {
		return nil
	}
	s.tracksLoading = a.ID
	return m.loadSearchTracks(a.ID)
}

// --- update -----------------------------------------------------------------

func (m *Model) updateSearch(msg tea.Msg) tea.Cmd {
	s := m.search
	if s == nil {
		return nil
	}
	switch msg := msg.(type) {
	case searchDebounceMsg:
		if msg.seq != s.seq {
			return nil
		}
		return m.runSearch()

	case searchResultMsg:
		if msg.query != strings.TrimSpace(s.query) {
			return nil // typed on since
		}
		s.running, s.ran, s.err = false, msg.query, msg.err
		if msg.err != nil {
			return m.flash("search: "+msg.err.Error(), true)
		}
		s.artists, s.results = msg.res.Artists, msg.res.Albums
		labels := []string{matchingAlbums}
		for _, a := range s.artists {
			labels = append(labels, a.Name)
		}
		s.panes[paneArtists].filter = ""
		s.panes[paneArtists].setItems(labels)
		s.albumsWant = "-" // force refresh
		return m.searchArtistChanged()

	case searchAlbumsLoadMsg:
		if msg.artistID != s.albumsWant || s.albumsFor == msg.artistID {
			return nil
		}
		s.albumsLoading = true
		return m.loadSearchAlbums(msg.artistID)

	case searchAlbumsMsg:
		if msg.artistID != s.albumsWant {
			return nil
		}
		s.albumsLoading, s.albumsErr = false, msg.err
		if msg.err != nil {
			return m.flash("albums: "+msg.err.Error(), true)
		}
		// Oldest first, like the library; undated releases last.
		sort.SliceStable(msg.albums, func(i, j int) bool {
			a, b := msg.albums[i].ReleaseDate, msg.albums[j].ReleaseDate
			if (a == "") != (b == "") {
				return b == ""
			}
			return a < b
		})
		m.setSearchAlbums(msg.artistID, msg.albums)
		return m.searchAlbumChanged()

	case searchTracksLoadMsg:
		if msg.albumID != s.tracksWant || msg.albumID == s.tracksFor || msg.albumID == s.tracksLoading {
			return nil
		}
		s.tracksLoading = msg.albumID
		return m.loadSearchTracks(msg.albumID)

	case searchTracksMsg:
		if msg.albumID == s.tracksLoading {
			s.tracksLoading = ""
		}
		var cmd tea.Cmd
		startID := ""
		if pp := s.pendingPlay; pp != nil && pp.key == msg.albumID {
			s.pendingPlay = nil
			if msg.err == nil {
				album := applemusic.CatalogAlbum{}
				for _, a := range s.albums {
					if a.ID == msg.albumID {
						album = a
					}
				}
				cmd = m.playTracks(msg.tracks, []library.Album{catalogAlbum(album)}, pp.trackID)
				startID = pp.trackID
				if startID == "" && len(msg.tracks) > 0 {
					startID = msg.tracks[0].ID
				}
			}
		}
		if msg.albumID != s.tracksWant {
			return cmd
		}
		var album library.Album
		for _, a := range s.albums {
			if a.ID == msg.albumID {
				album = catalogAlbum(a)
			}
		}
		s.tracksFor, s.tracks, s.tracksErr = msg.albumID, msg.tracks, msg.err
		s.trackRows = buildTrackRows(msg.tracks, []library.Album{album}, false)
		labels := make([]string, len(s.trackRows))
		for i, r := range s.trackRows {
			if r.kind == rowTrack {
				labels[i] = s.tracks[r.track].Title
			} else {
				labels[i] = "All"
			}
		}
		s.panes[paneTracks].setItems(labels)
		if startID != "" {
			selectRow(s.panes[paneTracks], s.trackRows, s.tracks, startID)
		}
		if msg.err != nil {
			return tea.Batch(cmd, m.flash("tracks: "+msg.err.Error(), true))
		}
		return cmd

	case removedMsg:
		for _, list := range [][]applemusic.CatalogAlbum{s.albums, s.results} {
			for i := range list {
				if list[i].LibraryID == msg.album.ID {
					list[i].LibraryID = ""
				}
			}
		}
		return nil

	case addedMsg:
		delete(s.adding, msg.catalogID)
		if msg.err != nil {
			return m.flash("could not add to library: "+msg.err.Error(), true)
		}
		for _, list := range [][]applemusic.CatalogAlbum{s.albums, s.results} {
			for i := range list {
				if list[i].ID == msg.catalogID {
					list[i].LibraryID = msg.album.ID
				}
			}
		}
		return tea.Batch(m.refreshLibrary(), m.flash("added “"+msg.album.Title+"” to your library", false))
	}
	return nil
}

// selectRow puts p's cursor on track id's row.
func selectRow(p *pane, rows []trackRow, tracks []library.Track, id string) {
	for i, r := range rows {
		if r.kind == rowTrack && tracks[r.track].ID == id {
			p.selectIndex(i)
			return
		}
	}
}

// --- keys -------------------------------------------------------------------

func (m *Model) handleSearchKey(msg tea.KeyPressMsg, k string) tea.Cmd {
	s := m.search
	if s.editing {
		switch k {
		case "esc":
			if s.ran == "" {
				m.closeSearch()
			}
			s.editing = false
			return nil
		case "enter", "down", "tab":
			s.editing = false
			if len(s.artists) == 0 && len(s.results) > 0 {
				s.focus = paneAlbums
			} else {
				s.focus = paneArtists
			}
			s.seq++ // cancel a pending debounce; run now
			return m.runSearch()
		case "backspace":
			if r := []rune(s.query); len(r) > 0 {
				s.query = string(r[:len(r)-1])
			}
		case "ctrl+u":
			s.query = ""
		default:
			t := msg.Text
			if t == "" && k == "space" {
				t = " "
			}
			if t == "" || strings.IndexFunc(t, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
				return nil
			}
			s.query += t
		}
		s.seq++
		seq := s.seq
		if len([]rune(strings.TrimSpace(s.query))) < 2 {
			return nil
		}
		return tea.Tick(searchDebounce, func(time.Time) tea.Msg { return searchDebounceMsg{seq} })
	}

	switch k {
	case "esc":
		m.closeSearch()
		return nil
	case "s", "/":
		s.editing = true
		return nil
	case "a":
		return m.addSelected()
	case "D":
		a, ok := s.selectedAlbum()
		if s.focus == paneTracks || !ok {
			a, ok = s.shownAlbum()
		}
		if !ok {
			return nil
		}
		if a.LibraryID == "" {
			return m.flash("“"+a.Title+"” is not in your library", false)
		}
		return m.confirmDelete(library.Album{ID: a.LibraryID, CatalogID: a.ID, Title: a.Title, Artist: a.Artist})
	}
	if cmd, ok := m.playbackKey(k); ok {
		return cmd
	}
	p := s.panes[s.focus]
	switch k {
	case "tab", "l":
		s.focus = (s.focus + 1) % numPanes
		return nil
	case "shift+tab", "h":
		s.focus = (s.focus + numPanes - 1) % numPanes
		return nil
	case "1", "2", "3":
		s.focus = int(k[0] - '1')
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
	case "enter":
		switch s.focus {
		case paneArtists:
			s.focus = paneAlbums
			return nil
		case paneAlbums:
			cmd := m.playSearchSelection()
			s.focus = paneTracks
			if a, ok := s.selectedAlbum(); ok && a.ID == s.tracksFor && len(s.tracks) > 0 {
				selectRow(s.panes[paneTracks], s.trackRows, s.tracks, s.tracks[0].ID)
			}
			return cmd
		}
		return m.playSearchSelection()
	default:
		return nil
	}
	switch s.focus {
	case paneArtists:
		return m.searchArtistChanged()
	case paneAlbums:
		return m.searchAlbumChanged()
	}
	return nil
}

// --- view -------------------------------------------------------------------

func (m *Model) renderSearch() string {
	s := m.search
	var b strings.Builder
	prompt := " " + stTitleFocus.Render("Search Apple Music") + stDim.Render(" › ") + stBold.Render(s.query)
	if s.editing {
		prompt += "▏"
	}
	var status string
	switch {
	case s.running:
		status = "searching…"
	case s.err != nil:
		status = "search failed"
	case s.ran != "":
		status = fmt.Sprintf("%d artists · %d albums", len(s.artists), len(s.results))
	case s.editing:
		status = "type, enter to search · esc back"
	}
	b.WriteString(leftRight(prompt, stDim.Render(status+"  esc library "), m.width))
	b.WriteByte('\n')

	sep := stSep.Render("│")
	w0, w1, w2 := m.columnWidths()
	h := m.columnHeight()
	focusOf := func(i int) bool { return !s.editing && s.focus == i }

	artists := m.renderColumn(s.panes[paneArtists], w0, h, focusOf(paneArtists), false, "", func(idx int) cell {
		if idx == 0 {
			return cell{left: matchingAlbums, right: strconv.Itoa(len(s.results))}
		}
		a := s.artists[idx-1]
		return cell{left: a.Name, playing: m.playingAlbum.ID != "" && library.ArtistKey(a.Name) == library.ArtistKey(m.playingAlbum.Artist)}
	})

	albumsEmpty := ""
	switch {
	case s.albumsLoading:
		albumsEmpty = "loading…"
	case s.albumsErr != nil:
		albumsEmpty = "could not load albums"
	case s.ran != "" && len(s.albums) == 0 && s.albumsFor == s.albumsWant:
		albumsEmpty = "no albums"
	}
	albums := m.renderColumn(s.panes[paneAlbums], w1, h, focusOf(paneAlbums), false, albumsEmpty, func(idx int) cell {
		a := s.albums[idx]
		c := cell{left: s.panes[paneAlbums].labels[idx], playing: a.ID == m.playingAlbum.ID}
		switch {
		case s.adding[a.ID]:
			c.right = "adding…"
		case m.removing[a.LibraryID]:
			c.right = "removing…"
		case a.LibraryID != "":
			c.right = "✓ in library"
		case a.Single:
			c.right, c.rightDim = "single", true // only the tag is pale
		}
		return c
	})

	tracksEmpty := ""
	switch {
	case s.tracksErr != nil:
		tracksEmpty = "could not load tracks"
	case s.tracksWant != "" && s.tracksFor != s.tracksWant:
		tracksEmpty = "loading…"
	}
	nums := trackNumbers(s.tracks)
	tracks := m.withCover(w2, h, func(h int) []string {
		return m.renderColumn(s.panes[paneTracks], w2, h, focusOf(paneTracks), false, tracksEmpty, func(idx int) cell {
			return m.trackCell(s.trackRows, s.tracks, nums, false, idx)
		})
	})

	for row := 0; row < h+1; row++ {
		b.WriteString(artists[row] + sep + albums[row] + sep + tracks[row] + "\n")
	}
	b.WriteString(m.renderBar())
	return b.String()
}
