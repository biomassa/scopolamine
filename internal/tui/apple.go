package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/library"
)

// appleIdleAfter is how long the local view shows before the Apple Music
// player (headless Chrome) shuts down to free its memory. It starts again
// when the Apple view shows.
const appleIdleAfter = 10 * time.Minute

// The states of the Apple Music player.
const (
	appleOff = iota
	appleStarting
	appleReady
	appleStopping
)

type (
	// appleIdleMsg is the end of the idle time with the number of its timer.
	appleIdleMsg struct{ seq int }
	// appleStoppedMsg reports that the Apple Music player has shut down.
	appleStoppedMsg struct{}
)

// appleOnShow starts the Apple Music player when the Apple view shows, and
// the idle timer when the local view shows.
func (m *Model) appleOnShow(v *libView) tea.Cmd {
	m.appleIdleSeq++ // stops the timer that runs
	m.appleIdleDue = false
	if v.source == library.SourceApple {
		if m.appleState == appleOff && m.deps.StartApple != nil {
			m.appleState = appleStarting
			m.deps.StartApple()
		}
		return nil
	}
	return m.armAppleIdle()
}

// armAppleIdle starts the idle timer if the Apple Music player runs.
func (m *Model) armAppleIdle() tea.Cmd {
	if m.appleState != appleReady || m.deps.StopApple == nil {
		return nil
	}
	seq := m.appleIdleSeq
	return tea.Tick(appleIdleAfter, func(time.Time) tea.Msg { return appleIdleMsg{seq} })
}

// maybeStopApple shuts the Apple Music player down when the idle time is
// over and no Apple Music plays. A paused Apple track becomes the resume
// point of the Apple view.
func (m *Model) maybeStopApple() tea.Cmd {
	if !m.appleIdleDue || m.appleState != appleReady || m.source != library.SourceLocal {
		return nil
	}
	if !m.state.Local && m.state.Track != nil && (m.state.Playing || m.state.Loading) {
		return nil // stop when the music stops or pauses
	}
	m.appleIdleDue = false
	if pv := m.playingView(); pv == m.apple {
		m.keepResume(pv)
	}
	m.appleState = appleStopping
	stop := m.deps.StopApple
	return func() tea.Msg {
		stop()
		return appleStoppedMsg{}
	}
}

// onAppleStopped starts the player again if the Apple view shows now.
func (m *Model) onAppleStopped() tea.Cmd {
	m.appleState = appleOff
	if m.source == library.SourceApple {
		return m.appleOnShow(m.libView)
	}
	return nil
}
