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
		{"other track", paneTracks, "i.a2", "i.a1", false}, // the test moves the cursor up
	} {
		t.Run(c.name, func(t *testing.T) {
			fp := &fakePlayer{}
			sess := &Session{Apple: &ViewSession{Focus: c.focus, Artist: "Boards of Canada", AlbumID: "l.a", TrackID: c.trackID,
				PlayAlbumID: "l.a", PlayTrackID: "i.a2", PlayPosSec: 1200}}
			m := New(context.Background(), Deps{Store: fixtureStore(t), Player: fp, Resume: sess})
			m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
			drive(m, m.Init())
			if sel := m.selectedTrackID(); sel != "i.a2" && c.focus == paneTracks {
				t.Fatalf("start: cursor on %q, want the resume track", sel)
			}
			if c.wantStart == "i.a1" {
				key(m, "k")
			}
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
	if got := m.selectedTrackID(); m.focus != paneTracks || got != "i.a2" {
		t.Fatalf("cursor on %q (focus %d), want the continued track i.a2", got, m.focus)
	}
	playing(m, "i.a2", "Music Is Math", 95*time.Second)
	m.selectTrackRow("i.a2")
	key(m, "enter") // the track that plays: nothing
	if fp.plays != 1 || fp.ids != nil {
		t.Fatalf("playing track: plays = %d, PlayTracks(%v)", fp.plays, fp.ids)
	}
	key(m, "2")
	key(m, "enter") // its album row: nothing
	if fp.ids != nil {
		t.Fatalf("playing album row: PlayTracks(%v)", fp.ids)
	}
	key(m, "3")
	m.selectTrackRow("i.a1")
	key(m, "enter") // another track: from 0:00
	if len(fp.ids) != 2 || fp.ids[fp.start] != "i.a1" {
		t.Fatalf("other track: PlayTracks(%v, %d)", fp.ids, fp.start)
	}
}

// "All albums", and an album header in it, hold the resume point: enter
// continues it, with all the artist's tracks in the queue.
func TestEnterAllAlbumsContinues(t *testing.T) {
	for _, onHeader := range []bool{false, true} {
		fp := &fakePlayer{}
		sess := &Session{Apple: &ViewSession{Focus: paneTracks, Artist: "Boards of Canada", AlbumID: "l.a", TrackID: "i.a2",
			PlayAlbumID: "l.a", PlayTrackID: "i.a2", PlayPosSec: 1200}}
		m := New(context.Background(), Deps{Store: fixtureStore(t), Player: fp, Resume: sess})
		m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
		drive(m, m.Init())
		key(m, "2")
		key(m, "g") // "All albums"
		if onHeader {
			key(m, "3")
			for m.panes[paneTracks].selected() >= 0 && m.trackRows[m.panes[paneTracks].selected()].kind != rowHeader ||
				m.trackRows[m.panes[paneTracks].selected()].album.ID != "l.a" {
				key(m, "j")
			}
		}
		key(m, "enter")
		if len(fp.ids) != 3 || fp.ids[fp.start] != "i.a2" {
			t.Fatalf("header=%v: PlayTracks(%v, %d), want all 3 tracks from i.a2", onHeader, fp.ids, fp.start)
		}
		m.Update(stateMsg{s: player.State{Playing: true, QueueIndex: fp.start, QueueLength: 3, Track: &player.NowPlaying{ID: "i.a2"}}, ok: true})
		if len(fp.seeks) != 1 || fp.seeks[0] != 20*time.Minute {
			t.Fatalf("header=%v: seeks = %v", onHeader, fp.seeks)
		}
	}
}

// selectedTrackID is the track under the cursor of the track column, or "".
func (m *Model) selectedTrackID() string {
	if i := m.panes[paneTracks].selected(); i >= 0 && i < len(m.trackRows) && m.trackRows[i].kind == rowTrack {
		return m.tracks[m.trackRows[i].track].ID
	}
	return ""
}

// A report from before the new queue (another track, not in the queue)
// does not drop the resume seek.
func TestResumeSeekIgnoresOldReports(t *testing.T) {
	fp := &fakePlayer{}
	sess := &Session{Apple: &ViewSession{Focus: paneTracks, Artist: "Boards of Canada", AlbumID: "l.a", TrackID: "i.a2",
		PlayAlbumID: "l.a", PlayTrackID: "i.a2", PlayPosSec: 1200}}
	m := New(context.Background(), Deps{Store: fixtureStore(t), Player: fp, Resume: sess})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	drive(m, m.Init())
	key(m, "enter")
	m.Update(stateMsg{s: player.State{Playing: true, Track: &player.NowPlaying{ID: "i.c1"}}, ok: true}) // an old report
	m.Update(stateMsg{s: player.State{Playing: true, QueueIndex: 1, QueueLength: 2, Track: &player.NowPlaying{ID: "i.a2"}}, ok: true})
	if len(fp.seeks) != 1 || fp.seeks[0] != 20*time.Minute {
		t.Fatalf("seeks = %v", fp.seeks)
	}
}
