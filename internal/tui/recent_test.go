package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/library"
)

// Recently added lists the new albums, newest first, with the artist and
// the added date, and without an "All albums" row.
func TestRecentlyAdded(t *testing.T) {
	ctx := context.Background()
	store := fixtureStore(t)
	now := time.Now().UTC()
	for _, a := range []library.Album{
		{ID: "l.a", Title: "Geogaddi", Artist: "Boards of Canada", ReleaseDate: "2002-02-18", TrackCount: 2, DateAdded: now.Format(time.RFC3339)},
		{ID: "l.c", Title: "Haha Sound", Artist: "Broadcast", ReleaseDate: "2003-08-04", TrackCount: 1, DateAdded: now.AddDate(0, 0, -3).Format(time.RFC3339)},
	} {
		if err := store.UpsertAlbum(ctx, library.SourceApple, a); err != nil {
			t.Fatal(err)
		}
	}
	fp := &fakePlayer{}
	m := New(ctx, Deps{Store: store, Player: fp})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	drive(m, m.Init())
	key(m, "j") // Recently added
	s := screen(m)
	if m.selectedArtist() != recentlyAdded || strings.Contains(s, allAlbums) || strings.Contains(s, "Music Has the Right") {
		t.Fatalf("recently added:\n%s", s)
	}
	geo, haha := strings.Index(s, "2002  Boards of Canada — Geogaddi"), strings.Index(s, "2003  Broadcast — Haha Sound")
	if geo < 0 || haha < geo || !strings.Contains(s, "today") || !strings.Contains(s, "3 days ago") {
		t.Fatalf("recent rows:\n%s", s)
	}

	// The first album is selected; enter plays it.
	key(m, "tab")
	key(m, "enter")
	if strings.Join(fp.ids, ",") != "i.a1,i.a2" {
		t.Fatalf("PlayTracks(%v)", fp.ids)
	}

	// o goes to the album under its artist.
	key(m, "o")
	if m.selectedArtist() != "Boards of Canada" || m.selectedAlbumID() != "l.a" {
		t.Fatalf("o: artist %q, album %q", m.selectedArtist(), m.selectedAlbumID())
	}

	// r goes back to Recently added, on the newest album, and clears the
	// filter of the artist column.
	key(m, "1")
	m.panes[paneArtists].setFilter("broad")
	key(m, "r")
	if m.selectedArtist() != recentlyAdded || m.focus != paneAlbums || m.selectedAlbumID() != "l.a" ||
		m.panes[paneArtists].filter != "" || !strings.Contains(screen(m), "r recent") {
		t.Fatalf("r: artist %q, focus %d, album %q\n%s", m.selectedArtist(), m.focus, m.selectedAlbumID(), screen(m))
	}
	key(m, "j") // Haha Sound
	key(m, "r")
	if m.selectedAlbumID() != "l.a" {
		t.Fatalf("r again: album %q", m.selectedAlbumID())
	}
}

func TestAddedAgo(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		days int
		want string
	}{
		{0, "today"}, {1, "yesterday"}, {3, "3 days ago"}, {13, "13 days ago"}, {14, "2 weeks ago"},
		{59, "8 weeks ago"}, {60, "2 months ago"}, {364, "12 months ago"}, {365, "1 year ago"}, {800, "2 years ago"},
	} {
		date := now.AddDate(0, 0, -tc.days).UTC().Format(time.RFC3339)
		if got := addedAgo(date, now); got != tc.want {
			t.Errorf("%d days: %q, want %q", tc.days, got, tc.want)
		}
	}
	if addedAgo("", now) != "" {
		t.Error("no date: want no text")
	}
}
