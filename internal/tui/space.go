package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/player"
)

// space pauses or resumes the track that plays, in any mode. When nothing
// plays, it continues the resume point of the mode that shows. It never
// plays the selection; enter does.
func (m *Model) space() tea.Cmd {
	if m.state.Track != nil {
		return m.withPlayer(func(p player.Player) error { return p.Toggle() })
	}
	if m.resume != nil {
		return m.resumePlayback()
	}
	return m.flash("push enter to play", false)
}
