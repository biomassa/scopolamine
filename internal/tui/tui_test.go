package tui

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/biomassa/scopolamine/internal/library"
	"github.com/biomassa/scopolamine/internal/player"
)

type fakePlayer struct {
	mu     sync.Mutex
	ids    []string
	start  int
	toggle int
	seeks  []time.Duration
	bc     player.Broadcast
}

func (f *fakePlayer) PlayTracks(ids []string, start int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids, f.start = ids, start
	return nil
}
func (f *fakePlayer) Play() error                    { return nil }
func (f *fakePlayer) Pause() error                   { return nil }
func (f *fakePlayer) Toggle() error                  { f.toggle++; return nil }
func (f *fakePlayer) Stop() error                    { return nil }
func (f *fakePlayer) Next() error                    { return nil }
func (f *fakePlayer) Previous() error                { return nil }
func (f *fakePlayer) Seek(d time.Duration) error     { f.seeks = append(f.seeks, d); return nil }
func (f *fakePlayer) SetVolume(float64) error        { return nil }
func (f *fakePlayer) State() player.State            { return player.State{} }
func (f *fakePlayer) Subscribe() <-chan player.State { return f.bc.Subscribe() }
func (f *fakePlayer) Close() error                   { return nil }

func fixtureStore(t *testing.T) *library.Store {
	t.Helper()
	ctx := context.Background()
	s, err := library.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	albums := []library.Album{
		{ID: "l.a", Title: "Geogaddi", Artist: "Boards of Canada", ReleaseDate: "2002-02-18", TrackCount: 2},
		{ID: "l.b", Title: "Music Has the Right to Children", Artist: "Boards of Canada", ReleaseDate: "1998-04-20", TrackCount: 1},
		{ID: "l.c", Title: "Haha Sound", Artist: "Broadcast", ReleaseDate: "2003-08-04", TrackCount: 1},
	}
	if err := s.ReplaceAlbums(ctx, library.SourceApple, albums); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.SetTracks(ctx, "l.a", []library.Track{
		{ID: "i.a2", Title: "Music Is Math", Artist: "Boards of Canada", Disc: 1, Number: 2, Duration: 321 * time.Second, Playable: true},
		{ID: "i.a1", Title: "Ready Lets Go", Artist: "Boards of Canada", Disc: 1, Number: 1, Duration: 60 * time.Second, Playable: true},
	}))
	must(s.SetTracks(ctx, "l.b", []library.Track{{ID: "i.b1", Title: "Wildlife Analysis", Disc: 1, Number: 1, Duration: 77 * time.Second, Playable: true}}))
	must(s.SetTracks(ctx, "l.c", []library.Track{{ID: "i.c1", Title: "Colour Me In", Disc: 1, Number: 1, Duration: 150 * time.Second, Playable: true}}))
	return s
}

// drive runs cmd and feeds resulting messages back until quiescent. Player
// subscriptions block forever, so batches are run with a timeout per command.
func drive(m *Model, cmd tea.Cmd) {
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(200 * time.Millisecond):
			continue // blocking watcher (player subscription, ticks)
		}
		if b, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, b...)
			continue
		}
		if msg == nil {
			continue
		}
		_, next := m.Update(msg)
		queue = append(queue, next)
	}
}

func key(m *Model, k string) {
	var msg tea.KeyPressMsg
	switch k {
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "space":
		msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "tab":
		msg = tea.KeyPressMsg{Code: tea.KeyTab}
	case "right":
		msg = tea.KeyPressMsg{Code: tea.KeyRight}
	case "left":
		msg = tea.KeyPressMsg{Code: tea.KeyLeft}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		r := []rune(k)[0]
		msg = tea.KeyPressMsg{Code: r, Text: k}
	}
	_, cmd := m.Update(msg)
	drive(m, cmd)
}

func screen(m *Model) string { return ansi.Strip(m.View().Content) }

func playing(m *Model, id, title string, pos time.Duration) {
	m.Update(stateMsg{s: player.State{Playing: true, Position: pos, Volume: 0.8, QueueIndex: 1, QueueLength: 2,
		Track: &player.NowPlaying{ID: id, Title: title, Artist: "Boards of Canada", Album: "Geogaddi", Duration: 321 * time.Second}}, ok: true})
}

func TestBrowseAndPlay(t *testing.T) {
	fp := &fakePlayer{}
	m := New(context.Background(), Deps{Store: fixtureStore(t), Player: fp, Volume: 0.8})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	drive(m, m.Init())

	s := screen(m)
	for _, want := range []string{"Artists", "Albums", "Tracks", "All artists", "All albums", "Boards of Canada", "Broadcast", "vol 80%", "+/- vol",
		"pick an artist to list all of its tracks"} {
		if !strings.Contains(s, want) {
			t.Fatalf("screen missing %q:\n%s", want, s)
		}
	}
	for _, line := range strings.Split(s, "\n") {
		if w := ansi.StringWidth(line); w > 120 {
			t.Fatalf("line wider than terminal (%d): %q", w, line)
		}
	}

	// Boards of Canada: "All albums" is selected, so Tracks lists every
	// track grouped by album, oldest album first, numbered.
	key(m, "j")
	s = screen(m)
	for _, want := range []string{"1998  Music Has the Right", "2002  Geogaddi", "All", "3 · 7:38", "1. Wildlife Analysis", "1. Ready Lets Go", "2. Music Is Math"} {
		if !strings.Contains(s, want) {
			t.Fatalf("grouped tracks missing %q:\n%s", want, s)
		}
	}
	if strings.Index(s, "Wildlife Analysis") > strings.Index(s, "Ready Lets Go") {
		t.Fatal("album groups not in year order")
	}

	// Enter on "All albums" plays every track of the artist.
	key(m, "tab")
	key(m, "enter")
	if strings.Join(fp.ids, ",") != "i.b1,i.a1,i.a2" || fp.start != 0 {
		t.Fatalf("all albums: PlayTracks(%v, %d)", fp.ids, fp.start)
	}
	// Enter on an album moves into Tracks, onto the first track.
	if m.focus != paneTracks {
		t.Fatalf("focus = %d after enter on album, want tracks", m.focus)
	}
	if r := m.trackRows[m.panes[paneTracks].selected()]; r.kind != rowTrack || m.tracks[r.track].ID != "i.b1" {
		t.Fatal("tracks cursor not on the first track")
	}

	// Second album (Geogaddi) on its own.
	key(m, "2")
	key(m, "j")
	key(m, "j")
	key(m, "enter")
	if strings.Join(fp.ids, ",") != "i.a1,i.a2" || fp.start != 0 || m.focus != paneTracks {
		t.Fatalf("album: PlayTracks(%v, %d) focus=%d", fp.ids, fp.start, m.focus)
	}

	// Tracks pane (cursor on track 1, below "All"): play from the second track.
	key(m, "j")
	key(m, "enter")
	if fp.start != 1 {
		t.Fatalf("start = %d, want 1", fp.start)
	}

	key(m, "space")
	if fp.toggle != 1 {
		t.Fatal("space did not toggle")
	}

	playing(m, "i.a2", "Music Is Math", 30*time.Second)
	s = screen(m)
	if !strings.Contains(s, "▶ 2. Music Is Math") || !strings.Contains(s, "0:30") || !strings.Contains(s, "2/2") {
		t.Fatalf("now playing not shown:\n%s", s)
	}

	// Arrows seek, tab switches columns.
	key(m, "right")
	key(m, "left")
	key(m, "left")
	if len(fp.seeks) != 3 || fp.seeks[0] != 40*time.Second || fp.seeks[2] != 20*time.Second {
		t.Fatalf("seeks = %v", fp.seeks)
	}
	if m.focus != paneTracks {
		t.Fatal("arrows changed the focused column")
	}
	key(m, "tab")
	if m.focus != paneArtists {
		t.Fatal("tab did not wrap to the artists column")
	}
}

func TestSessionRestore(t *testing.T) {
	store := fixtureStore(t)
	fp := &fakePlayer{}
	m := New(context.Background(), Deps{Store: store, Player: fp})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	drive(m, m.Init())
	key(m, "j")   // Boards of Canada
	key(m, "tab") // albums
	key(m, "j")   // 1998
	key(m, "j")   // Geogaddi
	key(m, "tab") // tracks
	key(m, "j")
	key(m, "j") // Music Is Math
	key(m, "enter")
	playing(m, "i.a2", "Music Is Math", 95*time.Second)
	sess := m.Session()
	a := sess.Apple
	if sess.LastMode != "apple" || a == nil || a.Artist != "Boards of Canada" || a.AlbumID != "l.a" || a.TrackID != "i.a2" || a.Focus != paneTracks ||
		a.PlayAlbumID != "l.a" || a.PlayTrackID != "i.a2" || a.PlayPosSec != 95 {
		t.Fatalf("session = %+v", sess)
	}

	// Round-trip through the file, then start a new model from it.
	path := t.TempDir() + "/session.json"
	if err := sess.Save(path); err != nil {
		t.Fatal(err)
	}
	fp2 := &fakePlayer{}
	m2 := New(context.Background(), Deps{Store: store, Player: fp2, Resume: LoadSession(path)})
	m2.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	drive(m2, m2.Init())
	if m2.selectedArtist() != "Boards of Canada" || m2.selectedAlbumID() != "l.a" || m2.focus != paneTracks {
		t.Fatalf("restored artist=%q album=%q focus=%d", m2.selectedArtist(), m2.selectedAlbumID(), m2.focus)
	}
	if i := m2.panes[paneTracks].selected(); m2.tracks[m2.trackRows[i].track].ID != "i.a2" {
		t.Fatal("track cursor not restored")
	}
	s := screen(m2)
	if !strings.Contains(s, "Music Is Math") || !strings.Contains(s, "space resumes") || !strings.Contains(s, "1:35") {
		t.Fatalf("resume point not shown:\n%s", s)
	}

	// Space resumes: plays the album from that track, then seeks once it plays.
	key(m2, "space")
	if strings.Join(fp2.ids, ",") != "i.a1,i.a2" || fp2.start != 1 {
		t.Fatalf("resume: PlayTracks(%v, %d)", fp2.ids, fp2.start)
	}
	if len(fp2.seeks) != 0 {
		t.Fatal("seeked before the track started")
	}
	playing(m2, "i.a2", "Music Is Math", 0)
	if len(fp2.seeks) != 1 || fp2.seeks[0] != 95*time.Second {
		t.Fatalf("resume seeks = %v", fp2.seeks)
	}

	// "All albums" is remembered as such.
	key(m2, "2")
	key(m2, "g")
	if s := m2.Session(); s.Apple.AlbumID != sessionAllAlbums {
		t.Fatalf("all albums not saved: %+v", s)
	}
}

func TestFilter(t *testing.T) {
	m := New(context.Background(), Deps{Store: fixtureStore(t)})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 16})
	drive(m, m.Init())
	key(m, "/")
	for _, r := range "broad" {
		key(m, string(r))
	}
	s := screen(m)
	if !strings.Contains(s, "/broad") || !strings.Contains(s, "Haha Sound") {
		t.Fatalf("filter:\n%s", s)
	}
	if strings.Contains(s, "Boards of Canada") {
		t.Fatalf("filtered-out artist still listed:\n%s", s)
	}
	key(m, "esc")
	if !strings.Contains(screen(m), "Boards of Canada") {
		t.Fatal("esc did not clear the filter")
	}
}

func TestTabLeavesFilter(t *testing.T) {
	m := New(context.Background(), Deps{Store: fixtureStore(t)})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 16})
	drive(m, m.Init())
	key(m, "/")
	for _, r := range "broad" {
		key(m, string(r))
	}
	// Terminals report Tab with Text "\t"; it must move focus, not filter.
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "\t"})
	drive(m, cmd)
	if m.focus != paneAlbums || m.filtering {
		t.Fatalf("focus=%d filtering=%v, want albums column", m.focus, m.filtering)
	}
	if f := m.panes[paneArtists].filter; f != "broad" {
		t.Fatalf("artist filter = %q, want it kept", f)
	}
	if !strings.Contains(screen(m), "Haha Sound") {
		t.Fatal("albums of the filtered artist not shown")
	}
}

func TestKeyName(t *testing.T) {
	cases := []struct {
		msg  tea.KeyPressMsg
		want string
	}{
		{tea.KeyPressMsg{Code: tea.KeyTab, Text: "\t"}, "tab"},
		{tea.KeyPressMsg{Code: tea.KeyTab}, "tab"},
		{tea.KeyPressMsg{Code: tea.KeyEnter, Text: "\r"}, "enter"},
		{tea.KeyPressMsg{Code: tea.KeyEscape, Text: "\x1b"}, "esc"},
		{tea.KeyPressMsg{Code: '?', Text: "?", Mod: tea.ModShift}, "?"},
		{tea.KeyPressMsg{Code: 'j', Text: "j"}, "j"},
	}
	for _, c := range cases {
		if got := keyName(c.msg); got != c.want {
			t.Errorf("keyName(%+v) = %q, want %q", c.msg, got, c.want)
		}
	}
}

func TestUnavailableTracks(t *testing.T) {
	ctx := context.Background()
	store := fixtureStore(t)
	// Geogaddi: first track has no stream; Broadcast: nothing streams.
	if err := store.SetTracks(ctx, "l.a", []library.Track{
		{ID: "i.a1", Title: "Ready Lets Go", Disc: 1, Number: 1, Duration: time.Minute},
		{ID: "i.a2", Title: "Music Is Math", Disc: 1, Number: 2, Duration: time.Minute, Playable: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTracks(ctx, "l.c", []library.Track{{ID: "i.c1", Title: "Colour Me In", Disc: 1, Number: 1}}); err != nil {
		t.Fatal(err)
	}
	fp := &fakePlayer{}
	m := New(ctx, Deps{Store: store, Player: fp})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	drive(m, m.Init())
	key(m, "j") // Boards of Canada
	if !strings.Contains(screen(m), "unavailable") {
		t.Fatalf("unavailable track not marked:\n%s", screen(m))
	}
	key(m, "tab")
	key(m, "j")
	key(m, "j") // Geogaddi
	key(m, "enter")
	if strings.Join(fp.ids, ",") != "i.a2" || fp.start != 0 {
		t.Fatalf("unplayable track queued: PlayTracks(%v, %d)", fp.ids, fp.start)
	}

	key(m, "1")
	key(m, "j") // Broadcast
	key(m, "tab")
	key(m, "j")
	key(m, "enter")
	if !strings.Contains(screen(m), "not available in your Apple Music storefront") {
		t.Fatalf("no storefront message:\n%s", screen(m))
	}
}

func TestLegendWraps(t *testing.T) {
	for _, width := range []int{200, 110, 80} {
		m := New(context.Background(), Deps{Store: fixtureStore(t), Player: &fakePlayer{}})
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		drive(m, m.Init())
		lines := m.legendLines()
		wantLines := 1
		if width < 150 {
			wantLines = 2
		}
		if len(lines) != wantLines {
			t.Fatalf("width %d: %d legend lines, want %d: %q", width, len(lines), wantLines, lines)
		}
		s := screen(m)
		if got := strings.Count(s, "\n") + 1; got != 24 {
			t.Fatalf("width %d: screen has %d lines, want 24", width, got)
		}
		if width >= 110 {
			for _, it := range m.legendItems() {
				if !strings.Contains(s, it.key+" "+it.label) {
					t.Fatalf("width %d: legend lost %q:\n%s", width, it.key+" "+it.label, s)
				}
			}
		}
		// The keys use the theme accent, bold, and the labels the muted color.
		raw := m.View().Content
		if !strings.Contains(raw, stLegendKey.Render("enter")) || !strings.Contains(raw, stMuted.Render("play")) {
			t.Fatalf("width %d: legend keys not in the accent", width)
		}
	}
}

// A session file of version 0.2 (only Apple Music, flat fields) restores
// as the Apple Music session.
func TestLegacySession(t *testing.T) {
	path := t.TempDir() + "/session.json"
	legacy := `{"focus": 2, "artist": "Boards of Canada", "album_id": "l.a", "track_id": "i.a2",
		"play_album_id": "l.a", "play_track_id": "i.a2", "play_pos_sec": 95}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(context.Background(), Deps{Store: fixtureStore(t), Player: &fakePlayer{}, Resume: LoadSession(path)})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	drive(m, m.Init())
	if m.libView != m.apple || m.selectedArtist() != "Boards of Canada" || m.selectedAlbumID() != "l.a" || m.focus != paneTracks {
		t.Fatalf("legacy session not restored: artist=%q album=%q focus=%d", m.selectedArtist(), m.selectedAlbumID(), m.focus)
	}
	if !strings.Contains(screen(m), "space resumes") {
		t.Fatal("legacy resume point lost")
	}
}
