package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/library"
	"github.com/biomassa/scopolamine/internal/player"
)

// ViewSession is what is restored for one mode: where the cursor was, and
// what was playing (resumed with space or enter, never automatically).
type ViewSession struct {
	Focus   int    `json:"focus"`
	Artist  string `json:"artist,omitempty"`
	AlbumID string `json:"album_id,omitempty"`
	TrackID string `json:"track_id,omitempty"`
	Folders bool   `json:"folders,omitempty"` // local: folder sorting

	PlayAlbumID string  `json:"play_album_id,omitempty"`
	PlayTrackID string  `json:"play_track_id,omitempty"`
	PlayPosSec  float64 `json:"play_pos_sec,omitempty"`
}

// Session is restored on the next start: the mode that showed, and the
// state of each mode. The embedded ViewSession reads the files of version
// 0.2, which had only the Apple Music mode.
type Session struct {
	LastMode string       `json:"last_mode,omitempty"`
	Apple    *ViewSession `json:"apple,omitempty"`
	Local    *ViewSession `json:"local,omitempty"`
	ViewSession
}

// LoadSession reads path; a missing or unreadable file yields nil.
func LoadSession(path string) *Session {
	b, err := os.ReadFile(path) //nolint:gosec // our own cache file
	if err != nil {
		return nil
	}
	var s Session
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

// Save writes the session to path.
func (s Session) Save(path string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// resumePoint is the last session's playback, offered for resuming.
type resumePoint struct {
	albumID, trackID string
	pos              time.Duration
	album            library.Album
	track            *library.Track // nil until resumeInfoMsg arrives
}

type resumeInfoMsg struct {
	src   string
	album library.Album
	track library.Track
	ok    bool
}

// seekTarget is a seek to issue once trackID is actually playing.
type seekTarget struct {
	trackID string
	pos     time.Duration
}

func (m *Model) applySession(s *Session) {
	if s == nil {
		return
	}
	apple := s.Apple
	if apple == nil && s.LastMode == "" {
		legacy := s.ViewSession // a 0.2 session
		apple = &legacy
	}
	m.inView(m.apple, func() tea.Cmd { m.applyViewSession(apple); return nil })
	m.inView(m.local, func() tea.Cmd { m.applyViewSession(s.Local); return nil })
	if s.LastMode == library.SourceLocal && m.deps.ScanLocal != nil {
		m.libView = m.local
	}
}

func (m *Model) applyViewSession(s *ViewSession) {
	if s == nil {
		return
	}
	if s.Focus >= 0 && s.Focus < numPanes {
		m.focus = s.Focus
	}
	m.folders = s.Folders && m.source == library.SourceLocal
	m.wantArtist, m.wantAlbum, m.wantTrack = s.Artist, s.AlbumID, s.TrackID
	if s.PlayAlbumID != "" && s.PlayTrackID != "" {
		m.resume = &resumePoint{
			albumID: s.PlayAlbumID,
			trackID: s.PlayTrackID,
			pos:     time.Duration(s.PlayPosSec * float64(time.Second)),
		}
	}
}

// loadResumeInfo looks up the resumable track of the view in the cache for
// the bar.
func (m *Model) loadResumeInfo() tea.Cmd {
	r := m.resume
	if r == nil {
		return nil
	}
	store, src, folders := m.deps.Store, m.source, m.folders
	return func() tea.Msg {
		ctx := context.WithoutCancel(m.ctx)
		albums, err := albumsForKey(ctx, store, src, folders, r.albumID)
		if err != nil || len(albums) != 1 {
			return resumeInfoMsg{src: src}
		}
		a := albums[0]
		var tracks []library.Track
		if strings.HasPrefix(a.ID, library.FolderPrefix) {
			tracks, err = folderTracks(ctx, store, a)
		} else {
			tracks, _, err = store.Tracks(ctx, a.ID)
		}
		if err != nil {
			return resumeInfoMsg{src: src}
		}
		for _, t := range tracks {
			if t.ID == r.trackID {
				return resumeInfoMsg{src: src, album: a, track: t, ok: true}
			}
		}
		return resumeInfoMsg{src: src}
	}
}

func (m *Model) onResumeInfo(msg resumeInfoMsg) {
	if m.resume == nil {
		return
	}
	if !msg.ok {
		m.resume = nil // album or track no longer in the library
		return
	}
	m.resume.album = msg.album
	t := msg.track
	m.resume.track = &t
}

// resumePlayback plays the resume point's album from its track and seeks to
// the saved position once that track is playing.
func (m *Model) resumePlayback() tea.Cmd {
	r := m.resume
	if m.deps.Player == nil {
		return m.flash("player not ready yet", true)
	}
	if r.pos > 2*time.Second {
		m.pendingSeek = &seekTarget{trackID: r.trackID, pos: r.pos}
	}
	m.pendingPlay = &pendingPlay{key: r.albumID, trackID: r.trackID}
	return m.loadTracks(r.albumID)
}

// maybeSeek issues a pending resume seek once its track plays.
func (m *Model) maybeSeek(s player.State) {
	ps := m.pendingSeek
	if ps == nil || s.Track == nil {
		return
	}
	if s.Track.ID != ps.trackID {
		if s.Playing && !s.Loading {
			m.pendingSeek = nil // something else is playing; drop it
		}
		return
	}
	if s.Playing && !s.Loading && m.deps.Player != nil {
		m.pendingSeek = nil
		_ = m.deps.Player.Seek(ps.pos)
	}
}

// playingView is the view whose library plays now, or nil (nothing plays,
// or an album from the search plays).
func (m *Model) playingView() *libView {
	if m.state.Track == nil || m.playingAlbum.ID == "" || m.playingAlbum.Source == SourceCatalog {
		return nil
	}
	if m.state.Local {
		return m.local
	}
	return m.apple
}

// playingResume is the resume point of what plays now.
func (m *Model) playingResume() *resumePoint {
	if m.playingView() == nil {
		return nil
	}
	return &resumePoint{albumID: m.playingAlbum.ID, trackID: m.state.Track.ID, pos: m.state.Position, album: m.playingAlbum}
}

// keepResume makes what plays now the resume point of pv, when its music
// stops.
func (m *Model) keepResume(pv *libView) {
	pv.resume = m.playingResume()
	if pv.resume == nil {
		return
	}
	if t, ok := m.findTrack(pv, pv.resume.trackID); ok {
		pv.resume.track = &t
		return
	}
	// The view shows other tracks now: the player knows the track too. The
	// bar needs the track to show the resume point.
	n := m.state.Track
	pv.resume.track = &library.Track{ID: n.ID, AlbumID: pv.resume.albumID, Title: n.Title, Artist: n.Artist, Duration: n.Duration}
}

// Session captures the state of both modes for the next start.
func (m *Model) Session() Session {
	s := Session{LastMode: m.source}
	s.Apple = m.viewSession(m.apple)
	s.Local = m.viewSession(m.local)
	return s
}

func (m *Model) viewSession(v *libView) *ViewSession {
	var out *ViewSession
	m.inView(v, func() tea.Cmd {
		s := &ViewSession{Focus: m.focus, Artist: m.selectedArtist(), AlbumID: m.selectedAlbumID(), Folders: m.folders}
		if m.allAlbumsSelected() {
			s.AlbumID = sessionAllAlbums
		}
		if i := m.panes[paneTracks].selected(); i >= 0 && i < len(m.trackRows) && m.trackRows[i].kind == rowTrack {
			s.TrackID = m.tracks[m.trackRows[i].track].ID
		}
		r := m.resume
		if m.playingView() == v {
			r = m.playingResume()
		}
		if r != nil {
			s.PlayAlbumID, s.PlayTrackID, s.PlayPosSec = r.albumID, r.trackID, r.pos.Seconds()
		}
		out = s
		return nil
	})
	return out
}
