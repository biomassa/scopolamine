package library_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/biomassa/scopolamine/internal/library"
)

func localFile(path, folder, artist, album string, year int, titles ...string) library.LocalFile {
	id := library.LocalAlbumID(artist, album, folder)
	f := library.LocalFile{
		Path:   path,
		Stamp:  library.FileStamp{Mtime: 1, Size: 1},
		Albums: []library.Album{{ID: id, Title: album, Artist: artist, Year: year}},
	}
	for i, t := range titles {
		f.Tracks = append(f.Tracks, library.Track{
			ID: library.LocalTrackID(path, 0), AlbumID: id, Title: t, Artist: artist, Number: i + 1,
			Duration: time.Minute, Playable: true, Path: path, Folder: folder, Codec: "flac", SampleRate: 44100, Bits: 16,
		})
	}
	return f
}

func TestLocalLibrary(t *testing.T) {
	ctx := context.Background()
	s, err := library.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	// An Apple album with a cached track must not mix with local albums.
	if err := s.ReplaceAlbums(ctx, library.SourceApple, []library.Album{{ID: "l.x", Title: "Apple Album", Artist: "Polwechsel"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTracks(ctx, "l.x", []library.Track{{ID: "i.x", Title: "Apple Track", Playable: true}}); err != nil {
		t.Fatal(err)
	}

	a := localFile("/m/koptt/Polwechsel - Embrace (2023)/01.flac", "koptt/Polwechsel - Embrace (2023)", "Polwechsel", "Embrace", 2023, "Embrace I")
	b := localFile("/m/koptt/Polwechsel - Embrace (2023)/02.flac", "koptt/Polwechsel - Embrace (2023)", "Polwechsel", "Embrace", 2023, "Embrace II")
	b.Tracks[0].Number = 2
	c := localFile("/m/SORGHO2/Field/1.flac", "SORGHO2/Field", "Polwechsel", "Field", 2009, "Place")
	if err := s.UpdateLocal(ctx, []library.LocalFile{a, b, c}, nil); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		source string
		want   int
	}{{library.SourceApple, 1}, {library.SourceLocal, 2}} {
		albums, err := s.AlbumsByArtist(ctx, tc.source, "Polwechsel")
		if err != nil || len(albums) != tc.want {
			t.Fatalf("%s albums = %d (%v), want %d", tc.source, len(albums), err, tc.want)
		}
	}
	if all, _ := s.AllAlbums(ctx, library.SourceLocal); len(all) != 2 || all[0].Title != "Field" || all[0].TrackCount != 1 || all[1].TrackCount != 2 {
		t.Fatalf("local albums: %+v", all)
	}
	if arts, _ := s.Artists(ctx, library.SourceApple); len(arts) != 1 || arts[0].AlbumCount != 1 {
		t.Fatalf("apple artists: %+v", arts)
	}

	// Folder mode.
	tops, err := s.FolderArtists(ctx)
	if err != nil || len(tops) != 2 || tops[0].Name != "koptt" || tops[1].Name != "SORGHO2" {
		t.Fatalf("folder tops: %+v %v", tops, err)
	}
	dirs, _ := s.FolderAlbums(ctx, "koptt")
	if len(dirs) != 1 || dirs[0].ID != library.FolderPrefix+"koptt/Polwechsel - Embrace (2023)" || dirs[0].Title != "Polwechsel - Embrace (2023)" || dirs[0].Year != 2023 {
		t.Fatalf("folder albums: %+v", dirs)
	}
	if all, _ := s.FolderAlbums(ctx, ""); len(all) != 2 {
		t.Fatalf("all folder albums: %+v", all)
	}
	ft, _ := s.FolderTracks(ctx, "koptt/Polwechsel - Embrace (2023)")
	if len(ft) != 2 || ft[0].Title != "Embrace I" || ft[1].Path != b.Path {
		t.Fatalf("folder tracks: %+v", ft)
	}

	stamps, _ := s.LocalFiles(ctx)
	if len(stamps) != 3 {
		t.Fatalf("stamps: %v", stamps)
	}

	// A cue sheet claims its audio file: the file's own track goes, the cue
	// tracks replace it.
	cue := library.LocalFile{Path: "/m/SORGHO2/Field/album.cue", Stamp: library.FileStamp{Mtime: 2, Size: 2}}
	cueAlbum := library.LocalAlbumID("Polwechsel", "Field", "SORGHO2/Field")
	cue.Albums = []library.Album{{ID: cueAlbum, Title: "Field", Artist: "Polwechsel", Year: 2009}}
	for i, title := range []string{"One", "Two"} {
		cue.Tracks = append(cue.Tracks, library.Track{
			ID: library.LocalTrackID(c.Path, i+1), AlbumID: cueAlbum, Title: title, Number: i + 1, Playable: true,
			Path: c.Path, Folder: "SORGHO2/Field", CueTrack: i + 1, Start: time.Duration(i) * 10 * time.Second,
		})
	}
	if err := s.UpdateLocal(ctx, []library.LocalFile{cue}, nil); err != nil {
		t.Fatal(err)
	}
	tr, _, _ := s.Tracks(ctx, cueAlbum)
	if len(tr) != 2 || tr[1].CueTrack != 2 || tr[1].Start != 10*time.Second {
		t.Fatalf("cue tracks: %+v", tr)
	}

	// Removing both Embrace files removes the album; the Apple data stays.
	if err := s.UpdateLocal(ctx, nil, []string{a.Path, b.Path}); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.AllAlbums(ctx, library.SourceLocal); len(all) != 1 {
		t.Fatalf("after removal: %+v", all)
	}
	if at, synced, _ := s.Tracks(ctx, "l.x"); !synced || len(at) != 1 || at[0].CueTrack != 0 || at[0].Path != "" {
		t.Fatalf("apple tracks changed: %+v", at)
	}
	if lt, err := s.LocalTrack(ctx, library.LocalTrackID(c.Path, 2)); err != nil || lt.Title != "Two" {
		t.Fatalf("local track: %+v %v", lt, err)
	}
}

// A database from 0.2.0 gets the local columns and keeps its Apple track
// cache.
func TestMigrateKeepsAppleTracks(t *testing.T) {
	path := t.TempDir() + "/v020.db"
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE albums (id TEXT PRIMARY KEY, source TEXT NOT NULL, catalog_id TEXT NOT NULL DEFAULT '', title TEXT NOT NULL,
			artist TEXT NOT NULL, artist_key TEXT NOT NULL, year INTEGER NOT NULL DEFAULT 0, release_date TEXT NOT NULL DEFAULT '',
			date_added TEXT NOT NULL DEFAULT '', track_count INTEGER NOT NULL DEFAULT 0, genre TEXT NOT NULL DEFAULT '',
			artwork_url TEXT NOT NULL DEFAULT '', tracks_synced_at INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE tracks (id TEXT NOT NULL, album_id TEXT NOT NULL, catalog_id TEXT NOT NULL DEFAULT '', title TEXT NOT NULL,
			artist TEXT NOT NULL, disc INTEGER NOT NULL DEFAULT 0, number INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0, playable INTEGER NOT NULL DEFAULT 1, PRIMARY KEY (album_id, id))`,
		`INSERT INTO albums (id, source, title, artist, artist_key, tracks_synced_at) VALUES ('l.x', 'apple', 'X', 'A', 'a', 5)`,
		`INSERT INTO tracks (id, album_id, title, artist, playable) VALUES ('i.1', 'l.x', 'T', 'A', 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	s, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tr, synced, err := s.Tracks(context.Background(), "l.x")
	if err != nil || !synced || len(tr) != 1 || tr[0].Title != "T" || tr[0].CueTrack != 0 {
		t.Fatalf("apple cache lost: %+v %v %v", tr, synced, err)
	}
}

func TestLocalSortIgnoresCase(t *testing.T) {
	ctx := context.Background()
	s, err := library.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var files []library.LocalFile
	for _, p := range []struct{ path, folder, album string }{
		{"/m/CaptainJam/x/b.flac", "CaptainJam/x", "x"},
		{"/m/awesome52/Alpine/A.flac", "awesome52/Alpine", "Alpine"},
		{"/m/bare foot/solo/1.flac", "bare foot/solo", "solo"},
		{"/m/BernardMarieKoltes/Tower/1.flac", "BernardMarieKoltes/Tower", "Tower"},
		{"/m/Ärger/zz/1.flac", "Ärger/zz", "zz"},
		{"/m/awesome52/beta/1.flac", "awesome52/beta", "beta"},
		{"/m/awesome52/Alpine/b.flac", "awesome52/Alpine", "Alpine"},
	} {
		f := localFile(p.path, p.folder, "Various", p.album, 2000, "t "+p.path)
		f.Tracks[0].Number = 0 // no track numbers: file order
		files = append(files, f)
	}
	if err := s.UpdateLocal(ctx, files, nil); err != nil {
		t.Fatal(err)
	}
	tops, _ := s.FolderArtists(ctx)
	var names []string
	for _, a := range tops {
		names = append(names, a.Name)
	}
	// Dictionary order: Ärger sorts with the A names (A-r before A-w).
	if got := join(names); got != "Ärger,awesome52,bare foot,BernardMarieKoltes,CaptainJam" {
		t.Fatalf("top folders: %s", got)
	}
	dirs, _ := s.FolderAlbums(ctx, "awesome52")
	if len(dirs) != 2 || dirs[0].Title != "Alpine" || dirs[1].Title != "beta" {
		t.Fatalf("album folders: %+v", dirs)
	}
	ft, _ := s.FolderTracks(ctx, "awesome52/Alpine")
	if len(ft) != 2 || ft[0].Path != "/m/awesome52/Alpine/A.flac" || ft[1].Path != "/m/awesome52/Alpine/b.flac" {
		t.Fatalf("file order: %s, %s", ft[0].Path, ft[1].Path)
	}
	albums, _ := s.AllAlbums(ctx, library.SourceLocal)
	var titles []string
	for _, a := range albums {
		titles = append(titles, a.Title)
	}
	if got := join(titles); got != "Alpine,beta,solo,Tower,x,zz" {
		t.Fatalf("album titles: %s", got)
	}
}

func join(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}

func TestLocalAlbumFormats(t *testing.T) {
	ctx := context.Background()
	s, err := library.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	a1 := localFile("/m/u/Same/1.flac", "u/Same", "X", "Same", 2000, "a")
	a2 := localFile("/m/u/Same/2.flac", "u/Same", "X", "Same", 2000, "b")
	m1 := localFile("/m/u/Mixed/1.flac", "u/Mixed", "X", "Mixed", 2001, "c")
	m2 := localFile("/m/u/Mixed/2.mp3", "u/Mixed", "X", "Mixed", 2001, "d")
	m2.Tracks[0].Codec, m2.Tracks[0].Bits = "mp3", 0
	if err := s.UpdateLocal(ctx, []library.LocalFile{a1, a2, m1, m2}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceAlbums(ctx, library.SourceApple, []library.Album{{ID: "l.x", Title: "Apple", Artist: "X"}}); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	albums, _ := s.AlbumsByArtist(ctx, library.SourceLocal, "X")
	for _, a := range albums {
		got[a.Title] = a.Format
	}
	if got["Same"] != "FLAC 44.1/16" || got["Mixed"] != library.MixedFormat {
		t.Fatalf("formats: %v", got)
	}
	dirs, _ := s.FolderAlbums(ctx, "u")
	if len(dirs) != 2 || dirs[0].Format != library.MixedFormat || dirs[1].Format != "FLAC 44.1/16" {
		t.Fatalf("folder formats: %+v", dirs)
	}
	if apple, _ := s.AlbumsByArtist(ctx, library.SourceApple, "X"); len(apple) != 1 || apple[0].Format != "" {
		t.Fatalf("apple album got a format: %+v", apple)
	}
}

func TestFormatLabel(t *testing.T) {
	for _, c := range []struct {
		t    library.Track
		want string
	}{
		{library.Track{Codec: "flac", SampleRate: 44100, Bits: 16}, "FLAC 44.1/16"},
		{library.Track{Codec: "alac", SampleRate: 96000, Bits: 24}, "ALAC 96/24"},
		{library.Track{Codec: "pcm_s24le", SampleRate: 88200, Bits: 24}, "PCM 88.2/24"},
		{library.Track{Codec: "mp3", SampleRate: 44100, Kbps: 320}, "MP3 320"},
		{library.Track{Codec: "mp3", SampleRate: 44100, Kbps: 245, VBR: true}, "MP3 ~245"},
		{library.Track{Codec: "opus", SampleRate: 48000, Kbps: 128, VBR: true}, "Opus ~128"},
		{library.Track{Codec: "vorbis", VBR: true}, "Vorbis VBR"},
	} {
		if got := library.FormatLabel(c.t); got != c.want {
			t.Errorf("FormatLabel(%+v) = %q, want %q", c.t, got, c.want)
		}
	}
}

// VBR tracks of one album show the length-weighted average; CBR tracks with
// different bitrates are mixed.
func TestAlbumVBRAverage(t *testing.T) {
	ctx := context.Background()
	s, err := library.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	mk := func(path, album string, kbps int, vbr bool, d time.Duration) library.LocalFile {
		f := localFile(path, "u/"+album, "X", album, 2000, path)
		f.Tracks[0].Codec, f.Tracks[0].Bits, f.Tracks[0].Kbps, f.Tracks[0].VBR, f.Tracks[0].Duration = "mp3", 0, kbps, vbr, d
		return f
	}
	if err := s.UpdateLocal(ctx, []library.LocalFile{
		mk("/v1", "V", 200, true, 3*time.Minute), mk("/v2", "V", 300, true, time.Minute),
		mk("/c1", "C", 320, false, time.Minute), mk("/c2", "C", 192, false, time.Minute),
	}, nil); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	albums, _ := s.AlbumsByArtist(ctx, library.SourceLocal, "X")
	for _, a := range albums {
		got[a.Title] = a.Format
	}
	if got["V"] != "MP3 ~225" || got["C"] != library.MixedFormat {
		t.Fatalf("formats: %v", got)
	}
}
