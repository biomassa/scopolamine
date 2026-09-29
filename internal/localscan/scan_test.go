package localscan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biomassa/scopolamine/internal/library"
)

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseCue(t *testing.T) {
	// Windows-1252 bytes: "Brötzmann" with ö = 0xF6.
	sheet := parseCue(string([]byte("REM DATE 1968\r\nPERFORMER \"Br\xf6tzmann\"\r\nTITLE \"Machine Gun\"\r\n")) +
		"FILE \"album.wav\" WAVE\n  TRACK 01 AUDIO\n    TITLE \"Machine Gun\"\n    INDEX 00 00:00:00\n    INDEX 01 00:00:32\n" +
		"  TRACK 02 AUDIO\n    TITLE \"Responsible\"\n    PERFORMER \"Other\"\n    INDEX 01 17:05:37\n")
	if sheet.date != "1968" || sheet.title != "Machine Gun" || len(sheet.files) != 1 || sheet.files[0].name != "album.wav" {
		t.Fatalf("sheet: %+v", sheet)
	}
	tr := sheet.files[0].tracks
	if len(tr) != 2 || tr[0].start != 32*time.Second/75 || tr[1].start != 17*time.Minute+5*time.Second+37*time.Second/75 || tr[1].performer != "Other" {
		t.Fatalf("tracks: %+v", tr)
	}

	dir := t.TempDir()
	write(t, filepath.Join(dir, "x.cue"), []byte("PERFORMER \"Br\xf6tzmann\"\nFILE \"a.wav\" WAVE\n TRACK 01 AUDIO\n INDEX 01 00:00:00\n TRACK 02 AUDIO\n INDEX 01 01:00:00\n"))
	c, err := readCue(filepath.Join(dir, "x.cue"))
	if err != nil || c.performer != "Brötzmann" {
		t.Fatalf("Windows-1252 cue: %q %v", c.performer, err)
	}
	// The sheet names a.wav; a.flac matches by its base name.
	write(t, filepath.Join(dir, "a.flac"), []byte("x"))
	if got := c.singleFile(dir); got != filepath.Join(dir, "a.flac") {
		t.Fatalf("singleFile = %q", got)
	}
	// Per-track sheets (one FILE per track) claim nothing.
	multi := parseCue("FILE \"1.flac\" WAVE\n TRACK 01 AUDIO\n INDEX 01 00:00:00\nFILE \"2.flac\" WAVE\n TRACK 02 AUDIO\n INDEX 01 00:00:00\n")
	if multi.singleFile(dir) != "" {
		t.Fatal("per-track sheet claimed a file")
	}
}

func TestParseProbe(t *testing.T) {
	pr, err := parseProbe([]byte(`{"format":{"duration":"61.5","tags":{"ALBUM ARTIST":"Polwechsel","TITLE":"Field","TRACK":"2/3","DATE":"2009-05-01"}},
		"streams":[{"codec_type":"audio","codec_name":"flac","sample_rate":"96000","bits_per_raw_sample":"24"},
		{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}]}`))
	if err != nil || pr.codec != "flac" || pr.sampleRate != 96000 || pr.bits != 24 || !pr.embeddedCover || pr.duration != 61500*time.Millisecond {
		t.Fatalf("probe: %+v %v", pr, err)
	}
	if first(pr.tags, "album_artist", "album artist") != "Polwechsel" {
		t.Fatal("tag keys not normalised")
	}
	mp3, _ := parseProbe([]byte(`{"format":{"duration":"1"},"streams":[{"codec_type":"audio","codec_name":"mp3","sample_rate":"44100","bits_per_sample":0}]}`))
	if mp3.bits != 0 {
		t.Fatal("lossy file got a bit depth")
	}
}

// TestScanReal uses ffmpeg/ffprobe on generated files: tags, folder-name
// fallbacks, folder covers, and a single-file album with a cue sheet.
func TestScanReal(t *testing.T) {
	for _, p := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(p); err != nil {
			t.Skip(p + " not installed")
		}
	}
	root := t.TempDir()
	gen := func(path string, secs string, meta ...string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		args := []string{"-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=" + secs, "-ac", "2", "-ar", "44100", "-sample_fmt", "s16"}
		for _, m := range meta {
			args = append(args, "-metadata", m)
		}
		if out, err := exec.Command("ffmpeg", append(args, path)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v %s", err, out)
		}
	}
	gen(filepath.Join(root, "userA", "Tagged Album", "01.flac"), "2", "title=First", "artist=Some Artist", "album_artist=Some Artist", "album=Tagged", "track=1", "date=2020")
	gen(filepath.Join(root, "userA", "Tagged Album", "02.flac"), "2", "title=Second", "artist=Some Artist", "album_artist=Some Artist", "album=Tagged", "track=2", "date=2020")
	write(t, filepath.Join(root, "userA", "Tagged Album", "cover.jpg"), []byte("jpeg"))
	gen(filepath.Join(root, "userB", "No Tags Here", "a track.flac"), "1")
	gen(filepath.Join(root, "userC", "Cue Album", "image.flac"), "30")
	write(t, filepath.Join(root, "userC", "Cue Album", "image.cue"), []byte(
		"PERFORMER \"Cue Artist\"\nTITLE \"Cue Album\"\nREM DATE 1999\nFILE \"image.wav\" WAVE\n"+
			"  TRACK 01 AUDIO\n    TITLE \"One\"\n    INDEX 01 00:00:00\n"+
			"  TRACK 02 AUDIO\n    TITLE \"Two\"\n    INDEX 01 00:10:00\n"+
			"  TRACK 03 AUDIO\n    TITLE \"Three\"\n    INDEX 01 00:20:00\n"))

	store, err := library.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	sc := &Scanner{Root: root}
	res, err := sc.Scan(ctx, store, nil)
	if err != nil || res.Files != 4 || res.Changed != 4 {
		t.Fatalf("scan: %+v %v", res, err)
	}

	byArtist := func(artist string) (library.Album, []library.Track) {
		t.Helper()
		albums, err := store.AlbumsByArtist(ctx, library.SourceLocal, artist)
		if err != nil || len(albums) != 1 {
			t.Fatalf("albums of %q: %+v %v", artist, albums, err)
		}
		tr, _, _ := store.Tracks(ctx, albums[0].ID)
		return albums[0], tr
	}
	a, tr := byArtist("Some Artist")
	if a.Title != "Tagged" || a.Year != 2020 || len(tr) != 2 || tr[0].Title != "First" || tr[1].Number != 2 ||
		tr[0].Codec != "flac" || tr[0].SampleRate != 44100 || tr[0].Bits != 16 || tr[0].Folder != "userA/Tagged Album" ||
		a.ArtworkURL != "file:"+filepath.Join(root, "userA", "Tagged Album", "cover.jpg") {
		t.Fatalf("tagged album: %+v %+v", a, tr)
	}
	// No tags: artist = the parent folder, album = the album folder, title = the file name.
	a, tr = byArtist("userB")
	if a.Title != "No Tags Here" || len(tr) != 1 || tr[0].Title != "a track" {
		t.Fatalf("untagged: %+v %+v", a, tr)
	}
	a, tr = byArtist("Cue Artist")
	if a.Title != "Cue Album" || a.Year != 1999 || len(tr) != 3 || tr[1].Title != "Two" || tr[1].CueTrack != 2 ||
		tr[1].Start != 10*time.Second || tr[2].Duration < 9*time.Second || tr[2].Duration > 11*time.Second ||
		tr[0].Path != filepath.Join(root, "userC", "Cue Album", "image.flac") || tr[0].CuePath != filepath.Join(root, "userC", "Cue Album", "image.cue") {
		t.Fatalf("cue album: %+v %+v", a, tr)
	}

	// A changed cue TITLE: one album with the new title, no old cue tracks.
	cuePath := filepath.Join(root, "userC", "Cue Album", "image.cue")
	cueText, _ := os.ReadFile(cuePath)
	write(t, cuePath, []byte(strings.Replace(string(cueText), `TITLE "Cue Album"`, `TITLE "Renamed Album"`, 1)))
	bump(t, cuePath)
	if _, err := sc.Scan(ctx, store, nil); err != nil {
		t.Fatal(err)
	}
	if albums, _ := store.AlbumsByArtist(ctx, library.SourceLocal, "Cue Artist"); len(albums) != 1 || albums[0].Title != "Renamed Album" || albums[0].TrackCount != 3 {
		t.Fatalf("after cue edit: %+v", albums)
	}
	// The cue sheet removed: the file is one plain track again, no cue tracks.
	if err := os.Remove(cuePath); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Scan(ctx, store, nil); err != nil {
		t.Fatal(err)
	}
	if albums, _ := store.AlbumsByArtist(ctx, library.SourceLocal, "Cue Artist"); len(albums) != 0 {
		t.Fatalf("cue album still there: %+v", albums)
	}
	plain, _ := store.FolderTracks(ctx, "userC/Cue Album")
	if len(plain) != 1 || plain[0].CueTrack != 0 || plain[0].Title != "image" {
		t.Fatalf("after cue removal: %+v", plain)
	}
	// A cue sheet again, then its audio file removed: nothing stays.
	write(t, cuePath, cueText)
	if _, err := sc.Scan(ctx, store, nil); err != nil {
		t.Fatal(err)
	}
	if tr, _ := store.FolderTracks(ctx, "userC/Cue Album"); len(tr) != 3 {
		t.Fatalf("cue tracks not back: %+v", tr)
	}
	if err := os.Remove(filepath.Join(root, "userC", "Cue Album", "image.flac")); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Scan(ctx, store, nil); err != nil {
		t.Fatal(err)
	}
	if tr, _ := store.FolderTracks(ctx, "userC/Cue Album"); len(tr) != 0 {
		t.Fatalf("tracks of a removed file stay: %+v", tr)
	}

	// No change → nothing read; a removed file → removed.
	if res, _ := sc.Scan(ctx, store, nil); res.Changed != 0 || res.Removed != 0 {
		t.Fatalf("rescan: %+v", res)
	}
	if err := os.Remove(filepath.Join(root, "userB", "No Tags Here", "a track.flac")); err != nil {
		t.Fatal(err)
	}
	if res, _ := sc.Scan(ctx, store, nil); res.Removed != 1 {
		t.Fatalf("after removal: %+v", res)
	}
	if albums, _ := store.AlbumsByArtist(ctx, library.SourceLocal, "userB"); len(albums) != 0 {
		t.Fatal("album of the removed file still there")
	}
}

func TestWatch(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changed := make(chan struct{}, 4)
	go func() { _ = Watch(ctx, root, func() { changed <- struct{}{} }) }()
	time.Sleep(100 * time.Millisecond)

	// A new folder with several files gives one call, after the quiet time.
	write(t, filepath.Join(root, "user", "album", "1.flac"), []byte("x"))
	write(t, filepath.Join(root, "user", "album", "2.flac"), []byte("x"))
	time.Sleep(500 * time.Millisecond)
	write(t, filepath.Join(root, "user", "album", "3.flac"), []byte("x")) // inside the new folder: watched too
	select {
	case <-changed:
	case <-time.After(watchQuiet + 3*time.Second):
		t.Fatal("no change reported")
	}
	select {
	case <-changed:
		t.Fatal("one burst of changes reported twice")
	case <-time.After(watchQuiet + 500*time.Millisecond):
	}
}

// bump moves a file's mtime forward, so that a rewrite in the same second
// still counts as a change.
func bump(t *testing.T, path string) {
	t.Helper()
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

// TestMP3VBR checks the VBR header detection on MP3 files from LAME: -b 320
// (CBR, an "Info" header) and -q:a 2 (VBR, a "Xing" header), with an ID3
// tag in front.
func TestMP3VBR(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	enc := func(name string, rate ...string) string {
		p := filepath.Join(dir, name)
		args := append([]string{"-loglevel", "error", "-f", "lavfi", "-i", "anoisesrc=d=5:c=pink", "-ac", "2", "-ar", "44100",
			"-c:a", "libmp3lame", "-metadata", "title=x"}, rate...)
		if out, err := exec.Command("ffmpeg", append(args, p)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v %s", err, out)
		}
		return p
	}
	cbr := enc("cbr.mp3", "-b:a", "320k")
	vbr := enc("vbr.mp3", "-q:a", "2")
	if v, _ := mp3VBR(cbr); v {
		t.Fatal("CBR file reported as VBR")
	}
	v, bytes := mp3VBR(vbr)
	if !v || bytes == 0 {
		t.Fatalf("VBR file: vbr=%v bytes=%d", v, bytes)
	}

	store, _ := library.Open(":memory:")
	defer func() { _ = store.Close() }()
	if _, err := (&Scanner{Root: dir}).Scan(context.Background(), store, nil); err != nil {
		t.Fatal(err)
	}
	tracks, _ := store.FolderTracks(context.Background(), "")
	labels := map[string]string{}
	for _, tr := range tracks {
		labels[filepath.Base(tr.Path)] = library.FormatLabel(tr)
	}
	if labels["cbr.mp3"] != "MP3 320" || !strings.HasPrefix(labels["vbr.mp3"], "MP3 ~") {
		t.Fatalf("labels: %v", labels)
	}
	t.Logf("labels: %v", labels)
}
