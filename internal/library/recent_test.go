package library_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/biomassa/scopolamine/internal/library"
)

func titles(albums []library.Album) []string {
	var out []string
	for _, a := range albums {
		out = append(out, a.Title)
	}
	return out
}

func sameTitles(got []library.Album, want ...string) bool {
	g := titles(got)
	if len(g) != len(want) {
		return false
	}
	for i := range g {
		if g[i] != want[i] {
			return false
		}
	}
	return true
}

// Recently added: the albums of the last 30 days, newest first; with none,
// the 50 newest.
func TestRecentAlbums(t *testing.T) {
	ctx := context.Background()
	s, err := library.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	date := func(daysAgo int) string { return now.AddDate(0, 0, -daysAgo).Format(time.RFC3339) }
	if err := s.ReplaceAlbums(ctx, library.SourceApple, []library.Album{
		{ID: "l.1", Title: "Old", Artist: "A", DateAdded: date(40)},
		{ID: "l.2", Title: "Week", Artist: "B", DateAdded: date(7)},
		{ID: "l.3", Title: "Today", Artist: "C", DateAdded: date(0)},
		{ID: "l.4", Title: "Undated", Artist: "D"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.RecentAlbums(ctx, library.SourceApple, now)
	if err != nil || !sameTitles(got, "Today", "Week") {
		t.Fatalf("last 30 days: %v %v", titles(got), err)
	}

	// Nothing in the last 30 days: the newest albums, at most 50.
	var albums []library.Album
	for i := range 60 {
		albums = append(albums, library.Album{ID: fmt.Sprintf("l.x%02d", i), Title: "T", Artist: "A", DateAdded: date(100 + i)})
	}
	if err := s.ReplaceAlbums(ctx, library.SourceApple, albums); err != nil {
		t.Fatal(err)
	}
	got, err = s.RecentAlbums(ctx, library.SourceApple, now)
	if err != nil || len(got) != library.RecentFallback || got[0].DateAdded != date(100) || got[49].DateAdded != date(149) {
		t.Fatalf("fallback: %d albums, %v", len(got), err)
	}
}

// Local files: the first scan of a folder dates each file by its
// modification time; later scans by the time they first see a file. A
// changed file keeps its date.
func TestLocalAddedDates(t *testing.T) {
	ctx := context.Background()
	s, err := library.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	old := time.Date(2020, 5, 1, 10, 0, 0, 0, time.UTC)
	a := localFile("/m/x/Embrace/1.flac", "x/Embrace", "Polwechsel", "Embrace", 2023, "Embrace I")
	a.Stamp.Mtime = old.UnixNano()
	if err := s.UpdateLocal(ctx, []library.LocalFile{a}, nil); err != nil {
		t.Fatal(err)
	}
	albums, err := s.AlbumsByArtist(ctx, library.SourceLocal, "Polwechsel")
	if err != nil || len(albums) != 1 || albums[0].DateAdded != "2020-05-01T10:00:00Z" {
		t.Fatalf("first scan: %+v %v", albums, err)
	}

	// A new file in a later scan: the scan time, even with an old mtime.
	b := localFile("/m/x/Field/1.flac", "x/Field", "Polwechsel", "Field", 2009, "Place")
	b.Stamp.Mtime = old.UnixNano()
	before := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	if err := s.UpdateLocal(ctx, []library.LocalFile{b}, nil); err != nil {
		t.Fatal(err)
	}
	// A changed file (new tags) keeps its date.
	a.Stamp.Size = 2
	a.Tracks[0].Title = "Embrace 1"
	if err := s.UpdateLocal(ctx, []library.LocalFile{a}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.RecentAlbums(ctx, library.SourceLocal, time.Now())
	if err != nil || !sameTitles(got, "Field") || got[0].DateAdded < before {
		t.Fatalf("later scan: %+v %v", got, err)
	}
	got, err = s.RecentAlbums(ctx, library.SourceLocal, old.AddDate(0, 0, 1))
	if err != nil || !sameTitles(got, "Field", "Embrace") || got[1].DateAdded != "2020-05-01T10:00:00Z" {
		t.Fatalf("changed file lost its date: %+v %v", got, err)
	}
	folders, err := s.RecentFolderAlbums(ctx, time.Now())
	if err != nil || len(folders) != 1 || folders[0].ID != library.FolderPrefix+"x/Field" {
		t.Fatalf("recent folders: %+v %v", folders, err)
	}

	// A new folder: all old files go, so the new files get their mtime.
	c := localFile("/n/y/Other/1.flac", "y/Other", "Someone", "Other", 2001, "One")
	c.Stamp.Mtime = old.UnixNano()
	if err := s.UpdateLocal(ctx, []library.LocalFile{c}, []string{a.Path, b.Path}); err != nil {
		t.Fatal(err)
	}
	got, err = s.RecentAlbums(ctx, library.SourceLocal, time.Now())
	if err != nil || !sameTitles(got, "Other") || got[0].DateAdded != "2020-05-01T10:00:00Z" {
		t.Fatalf("new folder: %+v %v", got, err)
	}
}

// A database from before 0.8.0 dates its local files by their modification
// time.
func TestMigrateAddedDates(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/v070.db"
	s, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a := localFile("/m/x/Embrace/1.flac", "x/Embrace", "Polwechsel", "Embrace", 2023, "Embrace I")
	a.Stamp.Mtime = time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC).UnixNano()
	if err := s.UpdateLocal(ctx, []library.LocalFile{a}, nil); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ALTER TABLE local_files DROP COLUMN added`, `UPDATE albums SET date_added = ''`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	if s, err = library.Open(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	albums, err := s.AlbumsByArtist(ctx, library.SourceLocal, "Polwechsel")
	if err != nil || len(albums) != 1 || albums[0].DateAdded != "2021-03-04T05:06:07Z" {
		t.Fatalf("migrated: %+v %v", albums, err)
	}
}
