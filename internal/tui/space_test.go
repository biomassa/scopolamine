package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/player"
)

func stopped(m *Model) { m.Update(stateMsg{s: player.State{QueueIndex: -1, Volume: 0.8}, ok: true}) }

func localPlaying(m *Model, paused bool) {
	m.Update(stateMsg{s: player.State{Playing: !paused, Local: true, QueueIndex: 0, QueueLength: 2, Position: 40 * time.Second,
		Track: &player.NowPlaying{ID: "file:/m/koptt/Embrace/1.flac", Title: "Embrace I", Duration: time.Minute}}, ok: true})
}

func newModesModel(t *testing.T) (*Model, *fakePlayer) {
	t.Helper()
	fp := &fakePlayer{}
	m := New(context.Background(), Deps{Store: localStore(t), Player: fp,
		ScanLocal: func(context.Context, func(int, int)) (int, error) { return 0, nil }})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	drive(m, m.Init())
	return m, fp
}

// playGeogaddi plays Geogaddi in the Apple Music mode, at 30 seconds of
// track 1.
func playGeogaddi(m *Model) {
	key(m, "j")
	key(m, "tab")
	key(m, "j")
	key(m, "j")
	key(m, "enter")
	playing(m, "i.a1", "Ready Lets Go", 30*time.Second)
}

// playEmbrace plays Embrace in the local mode.
func playEmbrace(m *Model) {
	key(m, "1")
	key(m, "g")
	key(m, "j")
	key(m, "tab")
	key(m, "j")
	key(m, "enter")
}

// space pauses and resumes what plays, in any mode. With nothing playing,
// it continues the resume point of the mode that shows.
func TestSpace(t *testing.T) {
	m, fp := newModesModel(t)
	key(m, "space")
	if fp.toggle != 0 || fp.ids != nil || !strings.Contains(screen(m), "push enter to play") {
		t.Fatalf("nothing to play: toggles = %d ids = %v\n%s", fp.toggle, fp.ids, screen(m))
	}
	playGeogaddi(m)
	key(m, "L")
	key(m, "space") // the other mode plays: pause it
	if fp.toggle != 1 || fp.stops != 0 {
		t.Fatalf("toggles = %d, stops = %d", fp.toggle, fp.stops)
	}

	// Local plays Embrace; Apple keeps track 1 at 0:30 as its resume point.
	playEmbrace(m)
	localPlaying(m, false)
	stopped(m) // the local music ends
	key(m, "L")
	fp.ids = nil
	key(m, "space") // nothing plays: the Apple resume point
	if len(fp.ids) != 2 || fp.ids[fp.start] != "i.a1" {
		t.Fatalf("continue: PlayTracks(%v, %d)", fp.ids, fp.start)
	}
	fp.seeks = nil
	m.Update(stateMsg{s: player.State{Playing: true, QueueIndex: 0, QueueLength: 2, Track: &player.NowPlaying{ID: "i.a1"}}, ok: true})
	if len(fp.seeks) != 1 || fp.seeks[0] != 30*time.Second {
		t.Fatalf("seeks = %v", fp.seeks)
	}
}

// A switch of mode puts the cursor on the current track of that mode: its
// resume point, with the resume time on the row, or its paused track.
func TestSwitchJumpsToCurrentTrack(t *testing.T) {
	m, fp := newModesModel(t)
	playGeogaddi(m)
	key(m, "2")
	key(m, "j") // the album cursor: Music Has the Right to Children
	key(m, "L")
	playEmbrace(m)
	playing(m, "i.a1", "Ready Lets Go", 31*time.Second) // Apple fades out
	localPlaying(m, false)

	// Back in Apple Music: the cursor is on Geogaddi, track 1, and the row
	// shows the resume time. The bar shows what plays (Embrace I).
	key(m, "L")
	if a, ok := m.selectedAlbum(); !ok || a.Title != "Geogaddi" || m.focus != paneTracks {
		t.Fatalf("album %+v, focus %d", a, m.focus)
	}
	s := screen(m)
	if !strings.Contains(s, "‖ 0:30 / 1:00") || !strings.Contains(s, "Embrace I") {
		t.Fatalf("resume row or bar:\n%s", s)
	}
	fp.seeks = nil
	key(m, "enter") // on the resume track: continue at 0:30
	if len(fp.ids) != 2 || fp.ids[fp.start] != "i.a1" {
		t.Fatalf("enter: PlayTracks(%v, %d)", fp.ids, fp.start)
	}
	m.Update(stateMsg{s: player.State{Playing: true, QueueIndex: 0, QueueLength: 2, Track: &player.NowPlaying{ID: "i.a1"}}, ok: true})
	if len(fp.seeks) != 1 || fp.seeks[0] != 30*time.Second {
		t.Fatalf("seeks = %v", fp.seeks)
	}

	// Pause Apple, move the cursor away, switch twice: back on the paused track.
	m.Update(stateMsg{s: player.State{Position: 40 * time.Second, QueueIndex: 0, QueueLength: 2,
		Track: &player.NowPlaying{ID: "i.a1", Title: "Ready Lets Go"}}, ok: true})
	key(m, "1")
	key(m, "j") // Broadcast
	key(m, "L")
	key(m, "L")
	if a, ok := m.selectedAlbum(); !ok || a.Title != "Geogaddi" || m.focus != paneTracks {
		t.Fatalf("paused: album %+v, focus %d", a, m.focus)
	}
}

// During the fade-out, the old track still sends states: they must not
// delete the resume point of its mode.
func TestResumePointSurvivesFade(t *testing.T) {
	m, _ := newModesModel(t)
	playGeogaddi(m)
	key(m, "L")
	playEmbrace(m)
	playing(m, "i.a1", "Ready Lets Go", 31*time.Second) // the fade-out
	if r := m.apple.resume; r == nil || r.trackID != "i.a1" || r.pos != 30*time.Second || r.track == nil {
		t.Fatalf("apple resume point = %+v", r)
	}
}
