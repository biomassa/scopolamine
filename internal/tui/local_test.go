package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/library"
	"github.com/biomassa/scopolamine/internal/player"
)

// localStore is the Apple fixture plus two local albums in two top folders.
func localStore(t *testing.T) *library.Store {
	t.Helper()
	s := fixtureStore(t)
	file := func(path, folder, artist, album string, year, n int, title string) library.LocalFile {
		id := library.LocalAlbumID(artist, album, folder)
		return library.LocalFile{
			Path: path, Stamp: library.FileStamp{Mtime: 1, Size: 1},
			Albums: []library.Album{{ID: id, Title: album, Artist: artist, Year: year}},
			Tracks: []library.Track{{ID: library.LocalTrackID(path, 0), AlbumID: id, Title: title, Artist: artist,
				Number: n, Duration: time.Minute, Playable: true, Path: path, Folder: folder, Codec: "flac", SampleRate: 44100, Bits: 16}},
		}
	}
	err := s.UpdateLocal(context.Background(), []library.LocalFile{
		file("/m/koptt/Embrace/1.flac", "koptt/Embrace", "Polwechsel", "Embrace", 2023, 1, "Embrace I"),
		file("/m/koptt/Embrace/2.flac", "koptt/Embrace", "Polwechsel", "Embrace", 2023, 2, "Embrace II"),
		file("/m/quqkuk/Perceptual Geography/1.flac", "quqkuk/Perceptual Geography", "Thomas Ankersmit", "Perceptual Geography", 2021, 1, "Perceptual Geography"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLocalMode(t *testing.T) {
	fp := &fakePlayer{}
	scans := 0
	m := New(context.Background(), Deps{Store: localStore(t), Player: fp,
		ScanLocal: func(context.Context, func(int, int)) (int, error) { scans++; return 0, nil }})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	drive(m, m.Init())
	if scans != 1 {
		t.Fatalf("local scans at start = %d, want 1", scans)
	}

	// Apple view: pick Boards of Canada, then switch to local.
	key(m, "j")
	if !strings.Contains(screen(m), "Apple Music") || strings.Contains(screen(m), "Polwechsel") {
		t.Fatalf("apple view:\n%s", screen(m))
	}
	key(m, "L")
	s := screen(m)
	for _, want := range []string{"local · metadata", "Polwechsel", "Thomas Ankersmit", "v sort", "L Apple Music"} {
		if !strings.Contains(s, want) {
			t.Fatalf("local view missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Boards of Canada") || strings.Contains(s, "s search") {
		t.Fatalf("apple items in the local view:\n%s", s)
	}

	// Album rows show their format at the right; the Apple rows have none.
	key(m, "j") // Polwechsel
	for _, l := range strings.Split(screen(m), "\n") {
		if strings.Contains(l, "2023  Embrace") {
			seg := l[:strings.LastIndex(l, "│")]
			if !strings.HasSuffix(strings.TrimRight(seg, " "), "FLAC 44.1/16") {
				t.Fatalf("format not right-aligned in the album column: %q", l)
			}
		}
	}
	key(m, "k")

	// Play the local album Embrace.
	key(m, "j") // Polwechsel
	key(m, "tab")
	key(m, "j") // Embrace
	key(m, "enter")
	if strings.Join(fp.ids, ",") != "file:/m/koptt/Embrace/1.flac,file:/m/koptt/Embrace/2.flac" {
		t.Fatalf("local PlayTracks(%v)", fp.ids)
	}
	m.Update(stateMsg{s: player.State{Playing: true, Local: true, Format: "FLAC 44.1/16", QueueIndex: 0, QueueLength: 2,
		Track: &player.NowPlaying{ID: "file:/m/koptt/Embrace/1.flac", Title: "Embrace I", Artist: "Polwechsel", Album: "Embrace", Duration: time.Minute}}, ok: true})
	if s := screen(m); !strings.Contains(s, "local · metadata · FLAC 44.1/16") || !strings.Contains(s, "▶ 1. Embrace I") {
		t.Fatalf("local playing:\n%s", s)
	}

	// s and D do nothing in local mode.
	key(m, "s")
	key(m, "D")
	if m.mode != modeLibrary || m.confirm != nil {
		t.Fatal("s or D acted in local mode")
	}

	// v: folder sorting — the top folders are the "artists".
	key(m, "v")
	s = screen(m)
	if !strings.Contains(s, "local · folders") || !strings.Contains(s, "koptt") || !strings.Contains(s, "quqkuk") {
		t.Fatalf("folder view:\n%s", s)
	}
	key(m, "1")
	key(m, "j") // koptt
	if !strings.Contains(screen(m), "Embrace") {
		t.Fatalf("folder albums:\n%s", screen(m))
	}

	// Back to Apple: the Apple selection is still Boards of Canada, and the
	// bar still shows the local track that plays.
	key(m, "L")
	s = screen(m)
	if m.selectedArtist() != "Boards of Canada" || !strings.Contains(s, "Apple Music · FLAC 44.1/16") || !strings.Contains(s, "Embrace I") {
		t.Fatalf("back to apple (artist %q):\n%s", m.selectedArtist(), s)
	}

	// o switches to the mode of the playing album and selects it.
	key(m, "o")
	if m.libView != m.local || m.selectedAlbumID() != library.LocalAlbumID("Polwechsel", "Embrace", "koptt/Embrace") {
		t.Fatalf("o: view local=%v album=%q", m.libView == m.local, m.selectedAlbumID())
	}

	// The session keeps both modes.
	sess := m.Session()
	if sess.LastMode != library.SourceLocal || sess.Local == nil || sess.Local.PlayTrackID != "file:/m/koptt/Embrace/1.flac" ||
		sess.Apple == nil || sess.Apple.Artist != "Boards of Canada" {
		t.Fatalf("session: %+v local=%+v apple=%+v", sess, sess.Local, sess.Apple)
	}
}
