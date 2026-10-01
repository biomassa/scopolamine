package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/applemusic"
)

type fakeCatalog struct {
	searches []string
	added    []string
	deleted  []string
	failAdd  map[string]bool
}

func (f *fakeCatalog) DeleteLibraryAlbum(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeCatalog) Search(_ context.Context, term string) (applemusic.SearchResult, error) {
	f.searches = append(f.searches, term)
	return applemusic.SearchResult{
		Artists: []applemusic.CatalogArtist{{ID: "art1", Name: "Loscil"}},
		Albums: []applemusic.CatalogAlbum{
			{Album: applemusic.Album{ID: "100", Title: "Plume", Artist: "Loscil", ReleaseDate: "2006-05-22"}, LibraryID: "l.p"},
		},
	}, nil
}

func (f *fakeCatalog) ArtistAlbums(context.Context, string) ([]applemusic.CatalogAlbum, error) {
	return []applemusic.CatalogAlbum{
		{Album: applemusic.Album{ID: "100", Title: "Plume", Artist: "Loscil", ReleaseDate: "2006-05-22"}, LibraryID: "l.p"},
		{Album: applemusic.Album{ID: "200", Title: "Submers", Artist: "Loscil", ReleaseDate: "2002-01-01"}, Single: true},
	}, nil
}

func (f *fakeCatalog) CatalogAlbumTracks(_ context.Context, id string) ([]applemusic.Track, error) {
	return []applemusic.Track{
		{ID: id + "1", Title: "Argo", Number: 1, Disc: 1, Duration: time.Minute, Playable: true},
		{ID: id + "2", Title: "Mistral", Number: 2, Disc: 1, Duration: time.Minute, Playable: true},
	}, nil
}

func (f *fakeCatalog) AddAlbumAndWait(_ context.Context, id string) (applemusic.Album, error) {
	f.added = append(f.added, id)
	if f.failAdd[id] {
		return applemusic.Album{}, errors.New("not available")
	}
	return applemusic.Album{ID: "l.new", CatalogID: id, Title: "Submers", Artist: "Loscil", ReleaseDate: "2002-01-01", TrackCount: 2}, nil
}

func typeText(m *Model, s string) {
	for _, r := range s {
		key(m, string(r))
	}
}

func TestSearch(t *testing.T) {
	store := fixtureStore(t)
	fp := &fakePlayer{}
	cat := &fakeCatalog{}
	m := New(context.Background(), Deps{Store: store, Player: fp, Catalog: cat})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 22})
	drive(m, m.Init())

	key(m, "s")
	if m.mode != modeSearch || !m.search.editing {
		t.Fatal("s did not open search input")
	}
	typeText(m, "loscil")
	key(m, "enter")
	if len(cat.searches) != 1 || cat.searches[0] != "loscil" {
		t.Fatalf("searches = %v (debounce should not double-run)", cat.searches)
	}
	s := screen(m)
	for _, want := range []string{"Search Apple Music", "loscil", "Matching albums", "Loscil", "2006  Loscil — Plume", "✓ in library"} {
		if !strings.Contains(s, want) {
			t.Fatalf("search screen missing %q:\n%s", want, s)
		}
	}

	// Artist → discography; second album is not in the library.
	key(m, "j")
	s = screen(m)
	if !strings.Contains(s, "2002  Submers") || !strings.Contains(s, "2006  Plume") {
		t.Fatalf("discography not shown:\n%s", s)
	}
	if strings.Index(s, "2002  Submers") > strings.Index(s, "2006  Plume") {
		t.Fatal("discography not oldest first")
	}
	// A single: the title in the normal color, only the "single" tag pale.
	raw := m.View().Content
	// (Submers has the cursor of the unfocused column, so its tag uses the
	// pale cursor-row style.)
	paleTag := strings.Contains(raw, stDim.Render(" single ")) || strings.Contains(raw, stSelPale.Render(" single "))
	if !strings.Contains(s, "single") || strings.Contains(raw, stDim.Render("  2002  Submers")) || !paleTag {
		t.Fatalf("single row style wrong:\n%s", s)
	}

	// Albums column: Submers (first), enter plays it and moves to Tracks.
	key(m, "tab")
	key(m, "enter")
	if strings.Join(fp.ids, ",") != "2001,2002" || fp.start != 0 || m.search.focus != paneTracks {
		t.Fatalf("PlayTracks(%v, %d) focus=%d", fp.ids, fp.start, m.search.focus)
	}
	if !strings.Contains(screen(m), "1. Argo") {
		t.Fatalf("catalog tracks not shown:\n%s", screen(m))
	}
	if m.playingAlbum.Source != SourceCatalog || m.playingAlbum.ID != "200" {
		t.Fatalf("playing album = %+v", m.playingAlbum)
	}

	// a adds the shown album; it lands in the library cache and is marked.
	key(m, "a")
	if len(cat.added) != 1 || cat.added[0] != "200" {
		t.Fatalf("added = %v", cat.added)
	}
	if a, err := store.Album(context.Background(), "l.new"); err != nil || a.Title != "Submers" {
		t.Fatalf("added album not in library cache: %+v %v", a, err)
	}
	if !strings.Contains(screen(m), "added “Submers” to your library") {
		t.Fatalf("no confirmation:\n%s", screen(m))
	}
	key(m, "2")
	if strings.Count(screen(m), "✓ in library") != 2 {
		t.Fatalf("both albums should be marked now:\n%s", screen(m))
	}
	key(m, "a") // already in library: no second add
	if len(cat.added) != 1 {
		t.Fatal("added twice")
	}

	// esc returns to the library, where the new album now appears.
	key(m, "esc")
	if m.mode != modeLibrary {
		t.Fatal("esc did not return to the library")
	}
	var names []string
	for _, a := range m.artists {
		names = append(names, a.Name)
	}
	if !strings.Contains(strings.Join(names, ","), "Loscil") {
		t.Fatalf("library artists not refreshed: %v", names)
	}
}

func TestRemoveFromLibrary(t *testing.T) {
	ctx := context.Background()
	store := fixtureStore(t)
	cat := &fakeCatalog{}
	m := New(ctx, Deps{Store: store, Player: &fakePlayer{}, Catalog: cat})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 22})
	drive(m, m.Init())

	// Library: Boards of Canada → Geogaddi, D asks, n cancels.
	key(m, "j")
	key(m, "tab")
	key(m, "j")
	key(m, "j")
	key(m, "D")
	if !strings.Contains(screen(m), "Remove “Geogaddi” by Boards of Canada from your Apple Music library? y/n") {
		t.Fatalf("no confirmation prompt:\n%s", screen(m))
	}
	key(m, "n")
	if len(cat.deleted) != 0 {
		t.Fatal("deleted without confirmation")
	}

	// D, y removes it from Apple and the cache; the view refreshes.
	key(m, "D")
	key(m, "y")
	if len(cat.deleted) != 1 || cat.deleted[0] != "l.a" {
		t.Fatalf("deleted = %v", cat.deleted)
	}
	if _, err := store.Album(ctx, "l.a"); err == nil {
		t.Fatal("album still in the cache")
	}
	s := screen(m)
	if strings.Contains(s, "2002  Geogaddi") || !strings.Contains(s, "removed “Geogaddi” from your library") {
		t.Fatalf("view not refreshed:\n%s", s)
	}

	// D on "All albums" or on an artist does nothing destructive.
	key(m, "g")
	key(m, "D")
	key(m, "y")
	key(m, "1")
	key(m, "D")
	key(m, "y")
	if len(cat.deleted) != 1 {
		t.Fatalf("removed more than asked: %v", cat.deleted)
	}

	// Search: Plume is in the library (l.p); D removes it and clears the mark.
	key(m, "s")
	typeText(m, "loscil")
	key(m, "enter")
	key(m, "2")
	key(m, "D")
	key(m, "y")
	if len(cat.deleted) != 2 || cat.deleted[1] != "l.p" {
		t.Fatalf("search delete = %v", cat.deleted)
	}
	if strings.Contains(screen(m), "✓ in library") {
		t.Fatalf("mark not cleared:\n%s", screen(m))
	}
	key(m, "D") // no longer in the library
	if m.confirm != nil {
		t.Fatal("asked to remove an album that is not in the library")
	}
}

// m and M mark albums of one artist; a adds all marked albums, one after the
// other; a failed album keeps its mark.
func TestMarkAndAddMany(t *testing.T) {
	store := fixtureStore(t)
	cat := &fakeCatalog{}
	m := New(context.Background(), Deps{Store: store, Player: &fakePlayer{}, Catalog: cat})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 22})
	drive(m, m.Init())
	key(m, "s")
	typeText(m, "loscil")
	key(m, "enter")
	key(m, "j")   // the artist Loscil: Submers (single), Plume (in library)
	key(m, "tab") // Albums
	s := m.search

	key(m, "m") // Submers: marked, the cursor moves to Plume
	key(m, "m") // Plume is in the library: no mark
	if len(s.marked) != 1 || !s.marked["200"] {
		t.Fatalf("marked = %v", s.marked)
	}
	if !strings.Contains(screen(m), "● 2002  Submers") || !strings.Contains(screen(m), "a add 1") {
		t.Fatalf("mark not shown:\n%s", screen(m))
	}
	key(m, "M") // marks exist: M clears them
	if len(s.marked) != 0 {
		t.Fatalf("M did not clear: %v", s.marked)
	}
	key(m, "M") // all that are not in the library, without singles: none here
	if len(s.marked) != 0 {
		t.Fatalf("M marked a single or a library album: %v", s.marked)
	}

	// Two marked albums; the first add fails and keeps its mark.
	s.albums = append(s.albums, applemusic.CatalogAlbum{Album: applemusic.Album{ID: "300", Title: "Endless Falls", Artist: "Loscil"}})
	key(m, "M") // an album, not in the library: marked
	if len(s.marked) != 1 || !s.marked["300"] {
		t.Fatalf("M marked %v, want only 300", s.marked)
	}
	s.marked["200"] = true
	cat.failAdd = map[string]bool{"200": true}
	key(m, "a")
	if len(cat.added) != 2 || cat.added[0] != "200" || cat.added[1] != "300" {
		t.Fatalf("added = %v", cat.added)
	}
	if !s.marked["200"] || s.marked["300"] || s.bulk != nil {
		t.Fatalf("marks after the add = %v, bulk = %v", s.marked, s.bulk)
	}
	if !strings.Contains(screen(m), "added 1 album · 1 failed") {
		t.Fatalf("no summary:\n%s", screen(m))
	}

	// Another artist clears the marks.
	key(m, "1")
	key(m, "k")
	if len(s.marked) != 0 {
		t.Fatalf("marks of another artist kept: %v", s.marked)
	}
}
