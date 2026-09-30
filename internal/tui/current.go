package tui

import "strings"

// The kinds of the current track of a mode.
const (
	curPlaying = iota
	curPaused
	curResume
)

// currentTrack is the current track of the mode that shows: the track that
// plays or is paused in this mode, else the resume point of the mode. id
// is "" if there is none.
func (m *Model) currentTrack() (id, albumID string, kind int) {
	if s := m.state; s.Track != nil && m.playingView() == m.libView {
		if s.Playing || s.Loading {
			return s.Track.ID, m.playingAlbum.ID, curPlaying
		}
		return s.Track.ID, m.playingAlbum.ID, curPaused
	}
	if r := m.resume; r != nil {
		return r.trackID, r.albumID, curResume
	}
	return "", "", 0
}

// rowHolds reports whether the row under the cursor, for tracks key, holds
// track id of album albumID: the track row itself, the album row or an
// album header of that album, or an "All" row of a list with the track.
func (m *Model) rowHolds(key, id, albumID string) bool {
	if m.focus == paneTracks && m.tracksFor == key {
		i := m.panes[paneTracks].selected()
		if i < 0 || i >= len(m.trackRows) {
			return false
		}
		switch r := m.trackRows[i]; r.kind {
		case rowTrack:
			return m.tracks[r.track].ID == id
		case rowHeader:
			return r.album.ID == albumID
		default: // the "All" row of the track column
			for _, t := range m.tracks {
				if t.ID == id {
					return true
				}
			}
			return false
		}
	}
	if key == albumID {
		return true
	}
	if strings.HasPrefix(key, artistKeyPrefix) { // "All albums" of the artist
		for _, a := range m.albums {
			if a.ID == albumID {
				return true
			}
		}
	}
	return false
}
