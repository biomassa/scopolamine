package tui

import (
	"context"
	"errors"
	"strings"
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

func TestAppleRetry(t *testing.T) {
	starts := 0
	m := New(context.Background(), Deps{Store: localStore(t), Player: &fakePlayer{},
		ScanLocal:  func(context.Context, func(int, int)) (int, error) { return 0, nil },
		StartApple: func() { starts++ },
		StopApple:  func() {}})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	drive(m, m.Init())
	if starts != 1 {
		t.Fatalf("starts = %d", starts)
	}

	// The first start fails: a hint for each mode.
	m.Update(PlayerStatusMsg("starting Apple Music…"))
	m.Update(AppleFailedMsg{Err: errors.New("no network")})
	m.notice = "" // the error notice hides the status line
	if s := screen(m); !strings.Contains(s, "playback unavailable · L L to try again") || strings.Contains(s, "starting Apple Music") {
		t.Fatalf("apple hint:\n%s", s)
	}
	key(m, "L")
	m.notice = ""
	if s := screen(m); !strings.Contains(s, "Apple Music unavailable · L to try again") {
		t.Fatalf("local hint:\n%s", s)
	}

	// The next switch to the Apple Music mode tries again.
	key(m, "L")
	if starts != 2 || m.appleState != appleStarting || m.appleFailed {
		t.Fatalf("retry: starts = %d, state = %d, failed = %v", starts, m.appleState, m.appleFailed)
	}
	m.notice = ""
	if strings.Contains(screen(m), "to try again") {
		t.Fatal("hint still shown during the retry")
	}

	// The retry fails after a restart too, and the switch after it retries.
	m.Update(AppleFailedMsg{Err: errors.New("timeout")})
	key(m, "L")
	key(m, "L")
	if starts != 3 {
		t.Fatalf("starts = %d", starts)
	}
	m.Update(PlayerReadyMsg{})
	if m.appleState != appleReady || m.appleFailed {
		t.Fatal("not ready after a good start")
	}
}
