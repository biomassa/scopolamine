package tui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/player"
)

func TestAppleIdleStop(t *testing.T) {
	starts, stops := 0, 0
	m := New(context.Background(), Deps{Store: localStore(t), Player: &fakePlayer{},
		ScanLocal:  func(context.Context, func(int, int)) (int, error) { return 0, nil },
		StartApple: func() { starts++ },
		StopApple:  func() { stops++ }})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	drive(m, m.Init())
	if starts != 1 || m.appleState != appleStarting {
		t.Fatalf("starts = %d, state = %d", starts, m.appleState)
	}
	m.Update(PlayerReadyMsg{})

	// Play Geogaddi, then switch to local while it plays.
	key(m, "j")
	key(m, "tab")
	key(m, "j")
	key(m, "j")
	key(m, "enter")
	playing(m, "i.a1", "Ready Lets Go", 20*time.Second)
	key(m, "L")

	// The idle time is over, but Apple Music plays: Chrome stays.
	_, cmd := m.Update(appleIdleMsg{m.appleIdleSeq})
	drive(m, cmd)
	if stops != 0 {
		t.Fatal("stopped while Apple Music plays")
	}

	// The music pauses: Chrome shuts down, and the paused track becomes the
	// resume point of the Apple view.
	_, cmd = m.Update(stateMsg{s: player.State{Position: 30 * time.Second, Volume: 0.8, QueueIndex: 0, QueueLength: 2,
		Track: &player.NowPlaying{ID: "i.a1", Title: "Ready Lets Go"}}, ok: true})
	drive(m, cmd)
	if stops != 1 || m.appleState != appleOff {
		t.Fatalf("stops = %d, state = %d", stops, m.appleState)
	}
	if r := m.apple.resume; r == nil || r.trackID != "i.a1" || r.pos != 30*time.Second || r.track == nil {
		t.Fatalf("resume point = %+v", r)
	}

	// The Apple view shows: Chrome starts again.
	key(m, "L")
	if starts != 2 || m.appleState != appleStarting {
		t.Fatalf("restart: starts = %d, state = %d", starts, m.appleState)
	}

	// Ready while the local view shows: the idle timer starts then.
	key(m, "L")
	if _, cmd := m.Update(PlayerReadyMsg{}); cmd == nil {
		t.Fatal("no idle timer when ready in the local view")
	}

	// A timer of an earlier visit to the local view does nothing.
	old := m.appleIdleSeq
	key(m, "L")
	key(m, "L")
	_, cmd = m.Update(appleIdleMsg{old})
	drive(m, cmd)
	if stops != 1 {
		t.Fatal("a stale timer stopped Chrome")
	}

	// No music: the current timer stops Chrome at once.
	_, cmd = m.Update(appleIdleMsg{m.appleIdleSeq})
	drive(m, cmd)
	if stops != 2 {
		t.Fatal("idle Chrome not stopped")
	}
}
