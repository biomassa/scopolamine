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

func TestSpaceAcrossModes(t *testing.T) {
	fp := &fakePlayer{}
	m := New(context.Background(), Deps{Store: localStore(t), Player: fp,
		ScanLocal: func(context.Context, func(int, int)) (int, error) { return 0, nil }})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	drive(m, m.Init())

	// Apple Music plays Geogaddi; switch to the local mode.
	key(m, "j")
	key(m, "tab")
	key(m, "j")
	key(m, "j")
	key(m, "enter")
	playing(m, "i.a1", "Ready Lets Go", 20*time.Second)
	key(m, "L")

	// First space: Apple Music stops and keeps its track as the resume point.
	key(m, "space")
	if fp.stops != 1 || fp.toggle != 0 {
		t.Fatalf("stops = %d, toggles = %d", fp.stops, fp.toggle)
	}
	if r := m.apple.resume; r == nil || r.trackID != "i.a1" || r.pos != 20*time.Second {
		t.Fatalf("apple resume point = %+v", r)
	}
	stopped(m)

	// Second space: the local mode has no current track. space never plays
	// the selection: it only gives a hint.
	fp.ids = nil
	key(m, "j") // Polwechsel
	key(m, "space")
	if fp.ids != nil || !strings.Contains(screen(m), "push enter to play") {
		t.Fatalf("no current track: ids = %v\n%s", fp.ids, screen(m))
	}
	key(m, "tab")
	key(m, "j") // Embrace
	key(m, "enter")
	if strings.Join(fp.ids, ",") != "file:/m/koptt/Embrace/1.flac,file:/m/koptt/Embrace/2.flac" {
		t.Fatalf("local enter: %v", fp.ids)
	}

	// The selection moves; space still pauses the track that plays.
	localPlaying(m, false)
	key(m, "k")
	key(m, "space")
	if fp.toggle != 1 || len(fp.ids) != 2 {
		t.Fatalf("toggles = %d, ids = %v", fp.toggle, fp.ids)
	}
	fp.toggle = 0

	// Back in Apple Music while local plays: stop, then the Apple resume point.
	localPlaying(m, false)
	key(m, "L")
	key(m, "space")
	if fp.stops != 2 || m.local.resume == nil || m.local.resume.trackID != "file:/m/koptt/Embrace/1.flac" {
		t.Fatalf("stops = %d, local resume = %+v", fp.stops, m.local.resume)
	}
	stopped(m)
	fp.ids = nil
	key(m, "space")
	if len(fp.ids) != 2 || fp.ids[fp.start] != "i.a1" {
		t.Fatalf("apple resume: ids = %v start = %d", fp.ids, fp.start)
	}

	// The other mode is paused: space plays this mode at once, no stop.
	m.Update(stateMsg{s: player.State{Position: 25 * time.Second, QueueIndex: 1, QueueLength: 2,
		Track: &player.NowPlaying{ID: "i.a1", Title: "Ready Lets Go", Duration: time.Minute}}, ok: true})
	key(m, "L")
	fp.ids = nil
	key(m, "space")
	if fp.stops != 2 || len(fp.ids) != 2 || fp.ids[fp.start] != "file:/m/koptt/Embrace/1.flac" {
		t.Fatalf("paused other mode: stops = %d ids = %v", fp.stops, fp.ids)
	}
	if r := m.apple.resume; r == nil || r.trackID != "i.a1" || r.pos != 25*time.Second {
		t.Fatalf("apple resume point after the paused switch = %+v", r)
	}
}

// During the fade-out, the old track still sends states. They must not
// delete the resume point of its mode: back in that mode, space continues
// at the saved position.
func TestResumePositionAfterFade(t *testing.T) {
	for _, via := range []string{"space", "enter"} {
		t.Run(via, func(t *testing.T) {
			fp := &fakePlayer{}
			m := New(context.Background(), Deps{Store: localStore(t), Player: fp,
				ScanLocal: func(context.Context, func(int, int)) (int, error) { return 0, nil }})
			m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
			drive(m, m.Init())
			key(m, "j")
			key(m, "tab")
			key(m, "j")
			key(m, "j")
			key(m, "enter") // Geogaddi
			playing(m, "i.a1", "Ready Lets Go", 20*time.Minute)
			key(m, "L")
			if via == "space" {
				key(m, "space") // stops Apple Music
			} else {
				key(m, "j")
				key(m, "tab")
				key(m, "j")
				key(m, "enter") // plays Embrace
			}
			playing(m, "i.a1", "Ready Lets Go", 20*time.Minute+time.Second) // the fade-out
			if r := m.apple.resume; r == nil || r.trackID != "i.a1" || r.pos != 20*time.Minute {
				t.Fatalf("apple resume point after the fade = %+v", r)
			}
			if via == "enter" {
				localPlaying(m, false)
			}
			stopped(m)
			key(m, "L")
			if via == "enter" {
				localPlaying(m, false)
				key(m, "space") // first push: stops the local music
				stopped(m)
			}
			fp.seeks = nil
			key(m, "space") // continues Apple Music
			if len(fp.ids) != 2 || fp.ids[fp.start] != "i.a1" {
				t.Fatalf("continue: ids = %v start = %d", fp.ids, fp.start)
			}
			m.Update(stateMsg{s: player.State{Loading: true, QueueIndex: 0, QueueLength: 2, Track: &player.NowPlaying{ID: "i.a1"}}, ok: true})
			m.Update(stateMsg{s: player.State{Playing: true, QueueIndex: 0, QueueLength: 2, Track: &player.NowPlaying{ID: "i.a1"}}, ok: true})
			if len(fp.seeks) != 1 || fp.seeks[0] != 20*time.Minute {
				t.Fatalf("seeks = %v, want [20m0s]", fp.seeks)
			}
		})
	}
}

// The cursor moves to another album before the switch: the resume point
// still shows in the bar, with the track of the player.
func TestResumeShownAfterCursorMoved(t *testing.T) {
	fp := &fakePlayer{}
	m := New(context.Background(), Deps{Store: localStore(t), Player: fp,
		ScanLocal: func(context.Context, func(int, int)) (int, error) { return 0, nil }})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	drive(m, m.Init())
	key(m, "j")
	key(m, "tab")
	key(m, "j")
	key(m, "j")
	key(m, "enter") // Geogaddi
	playing(m, "i.a1", "Ready Lets Go", 20*time.Minute)
	key(m, "2")
	key(m, "j") // the album cursor: Music Has the Right to Children
	key(m, "L")
	key(m, "space")
	stopped(m)
	key(m, "L")
	r := m.apple.resume
	if r == nil || r.track == nil || r.pos != 20*time.Minute {
		t.Fatalf("resume point = %+v", r)
	}
	if s := screen(m); !strings.Contains(s, "Ready Lets Go") || !strings.Contains(s, "space resumes") {
		t.Fatalf("bar does not show the resume point:\n%s", s)
	}
}
