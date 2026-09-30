package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/library"
	"github.com/biomassa/scopolamine/internal/player"
)

// space plays, pauses, or continues the current track of the mode that
// shows: the track that plays, or the resume point of the mode. It never
// plays the selection; enter does. When the other mode plays, the first
// space stops it (it keeps its track as its resume point), and the next
// space continues this mode. When the other mode is paused, one space is
// enough.
func (m *Model) space() tea.Cmd {
	s := m.state
	if s.Track == nil {
		return m.continueHere()
	}
	if s.Local == (m.source == library.SourceLocal) {
		return m.withPlayer(func(p player.Player) error { return p.Toggle() })
	}
	if s.Playing || s.Loading {
		if pv := m.playingView(); pv != nil {
			m.keepResume(pv)
		}
		m.playingAlbum = library.Album{}
		return m.withPlayer(func(p player.Player) error { return p.Stop() })
	}
	// Paused: playTracks keeps the other mode's track as its resume point.
	return m.continueHere()
}

// continueHere continues the resume point of the mode that shows.
func (m *Model) continueHere() tea.Cmd {
	if m.resume != nil {
		return m.resumePlayback()
	}
	return m.flash("push enter to play", false)
}
