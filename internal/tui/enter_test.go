package tui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/player"
)

// enter after a restart: on the album row or on the resume track, it
// continues at the saved position; on another track, it plays that track
// from the start.
func TestEnterContinuesResumePoint(t *testing.T) {
	for _, c := range []struct {
		name      string
		focus     int
		trackID   string // the track row with the cursor
		wantStart string
		wantSeek  bool
	}{
		{"album row", paneAlbums, "", "i.a2", true},
		{"resume track", paneTracks, "i.a2", "i.a2", true},
		{"other track", paneTracks, "i.a1", "i.a1", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			fp := &fakePlayer{}
			sess := &Session{Apple: &ViewSession{Focus: c.focus, Artist: "Boards of Canada", AlbumID: "l.a", TrackID: c.trackID,
				PlayAlbumID: "l.a", PlayTrackID: "i.a2", PlayPosSec: 1200}}
			m := New(context.Background(), Deps{Store: fixtureStore(t), Player: fp, Resume: sess})
			m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
			drive(m, m.Init())
			key(m, "enter")
			if len(fp.ids) <= fp.start || fp.ids[fp.start] != c.wantStart {
				t.Fatalf("PlayTracks(%v, %d), want start %s", fp.ids, fp.start, c.wantStart)
			}
			m.Update(stateMsg{s: player.State{Playing: true, QueueIndex: 0, QueueLength: 2, Track: &player.NowPlaying{ID: c.wantStart}}, ok: true})
			if got := len(fp.seeks) == 1 && fp.seeks[0] == 20*time.Minute; got != c.wantSeek {
				t.Fatalf("seeks = %v, want a seek to 20m: %v", fp.seeks, c.wantSeek)
			}
		})
	}
}

// enter on the album row of the paused album continues it; while it plays,
// enter on the album row starts it again.
func TestEnterOnPausedAlbum(t *testing.T) {
	fp := &fakePlayer{}
	m := New(context.Background(), Deps{Store: fixtureStore(t), Player: fp})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	drive(m, m.Init())
	key(m, "j")
	key(m, "tab")
	key(m, "j")
	key(m, "j")
	key(m, "enter") // Geogaddi
	key(m, "1")
	key(m, "2") // back on the album row
	m.Update(stateMsg{s: player.State{Position: 90 * time.Second, QueueIndex: 1, QueueLength: 2,
		Track: &player.NowPlaying{ID: "i.a2", Title: "Music Is Math"}}, ok: true}) // paused
	fp.ids = nil
	key(m, "enter")
	if fp.plays != 1 || fp.ids != nil {
		t.Fatalf("paused album: plays = %d, PlayTracks(%v)", fp.plays, fp.ids)
	}
	playing(m, "i.a2", "Music Is Math", 95*time.Second)
	key(m, "enter")
	if fp.plays != 1 || len(fp.ids) != 2 || fp.ids[fp.start] != "i.a1" {
		t.Fatalf("playing album: plays = %d, PlayTracks(%v, %d)", fp.plays, fp.ids, fp.start)
	}
}
