package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/library"
	"github.com/biomassa/scopolamine/internal/player"
)

// Session is what is restored on the next start: where the cursor was, and
// what was playing (resumed with space, never automatically).
type Session struct {
	Focus   int    `json:"focus"`
	Artist  string `json:"artist,omitempty"`
	AlbumID string `json:"album_id,omitempty"`
	TrackID string `json:"track_id,omitempty"`

	PlayAlbumID string  `json:"play_album_id,omitempty"`
	PlayTrackID string  `json:"play_track_id,omitempty"`
	PlayPosSec  float64 `json:"play_pos_sec,omitempty"`
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
	if s.Focus >= 0 && s.Focus < numPanes {
		m.focus = s.Focus
	}
	m.wantArtist, m.wantAlbum, m.wantTrack = s.Artist, s.AlbumID, s.TrackID
	if s.PlayAlbumID != "" && s.PlayTrackID != "" {
		m.resume = &resumePoint{
			albumID: s.PlayAlbumID,
			trackID: s.PlayTrackID,
			pos:     time.Duration(s.PlayPosSec * float64(time.Second)),
		}
	}
}

// loadResumeInfo looks up the resumable track in the cache for the bar.
func (m *Model) loadResumeInfo() tea.Cmd {
	r := m.resume
	if r == nil {
		return nil
	}
	store := m.deps.Store
	return func() tea.Msg {
		a, err := store.Album(m.ctx, r.albumID)
		if err != nil {
			return resumeInfoMsg{}
		}
		tracks, _, err := store.Tracks(context.WithoutCancel(m.ctx), r.albumID)
		if err != nil {
			return resumeInfoMsg{}
		}
		for _, t := range tracks {
			if t.ID == r.trackID {
				return resumeInfoMsg{album: a, track: t, ok: true}
			}
		}
		return resumeInfoMsg{}
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
	m.resume = nil
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

// Session captures the current position for the next start.
func (m *Model) Session() Session {
	s := Session{Focus: m.focus, Artist: m.selectedArtist(), AlbumID: m.selectedAlbumID()}
	if m.allAlbumsSelected() {
		s.AlbumID = sessionAllAlbums
	}
	if i := m.panes[paneTracks].selected(); i >= 0 && i < len(m.trackRows) && m.trackRows[i].kind == rowTrack {
		s.TrackID = m.tracks[m.trackRows[i].track].ID
	}
	switch {
	case m.state.Track != nil && m.playingAlbum.ID != "":
		s.PlayAlbumID = m.playingAlbum.ID
		s.PlayTrackID = m.state.Track.ID
		s.PlayPosSec = m.state.Position.Seconds()
	case m.resume != nil:
		// Nothing was played this time: keep offering the old resume point.
		s.PlayAlbumID, s.PlayTrackID, s.PlayPosSec = m.resume.albumID, m.resume.trackID, m.resume.pos.Seconds()
	}
	return s
}
