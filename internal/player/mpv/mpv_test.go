package mpv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/biomassa/scopolamine/internal/player"
)

func waitFor(t *testing.T, ch <-chan player.State, what string, ok func(player.State) bool) player.State {
	t.Helper()
	t0 := time.Now()
	defer func() { t.Logf("%-18s %s", what, time.Since(t0).Round(time.Millisecond)) }()
	deadline := time.After(8 * time.Second)
	for {
		select {
		case s, open := <-ch:
			if !open {
				t.Fatalf("%s: player closed", what)
			}
			if ok(s) {
				return s
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestMPV(t *testing.T) {
	for _, p := range []string{"mpv", "ffmpeg"} {
		if _, err := exec.LookPath(p); err != nil {
			t.Skip(p + " not installed")
		}
	}
	dir := t.TempDir()
	gen := func(name, secs string) string {
		p := filepath.Join(dir, name)
		out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration="+secs,
			"-ac", "2", "-ar", "44100", "-sample_fmt", "s16", p).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg: %v %s", err, out)
		}
		return p
	}
	a, b := gen("a.flac", "4"), gen("b.flac", "4")
	img := gen("image.flac", "30")
	cue := filepath.Join(dir, "image.cue")
	if err := os.WriteFile(cue, []byte("FILE \"image.flac\" WAVE\n  TRACK 01 AUDIO\n    TITLE \"One\"\n    INDEX 01 00:00:00\n"+
		"  TRACK 02 AUDIO\n    TITLE \"Two\"\n    INDEX 01 00:10:00\n  TRACK 03 AUDIO\n    TITLE \"Three\"\n    INDEX 01 00:20:00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	items := map[string]Item{
		"a":  {ID: "a", Path: a, Title: "A", Duration: 4 * time.Second, Codec: "flac", SampleRate: 44100, Bits: 16},
		"b":  {ID: "b", Path: b, Title: "B", Duration: 4 * time.Second, Codec: "flac", SampleRate: 44100, Bits: 16},
		"c1": {ID: "c1", Path: img, CuePath: cue, Start: 0, Duration: 10 * time.Second, Title: "One"},
		"c2": {ID: "c2", Path: img, CuePath: cue, Start: 10 * time.Second, Duration: 10 * time.Second, Title: "Two"},
		"c3": {ID: "c3", Path: img, CuePath: cue, Start: 20 * time.Second, Duration: 10 * time.Second, Title: "Three"},
	}
	if os.Getenv("MPV_DEBUG") != "" {
		debugLog = func(l string) { t.Log(l) }
		defer func() { debugLog = nil }()
	}
	p, err := New(context.Background(), Options{
		Resolve: func(id string) (Item, error) { return items[id], nil },
		Extra:   []string{"--ao=null"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	ch := p.Subscribe()

	if err := p.PlayTracks([]string{"a", "b", "c1", "c2", "c3"}, 1); err != nil {
		t.Fatal(err)
	}
	// While the earlier entries go in, no state may report another track:
	// the TUI then drops its resume seek.
	s := waitFor(t, ch, "track b", func(s player.State) bool {
		if s.Track != nil && s.Track.ID != "b" {
			t.Fatalf("start at b: a state reports track %s: %+v", s.Track.ID, s)
		}
		return s.Track != nil && s.Track.ID == "b" && s.Playing && !s.Loading
	})
	if s.QueueIndex != 1 || s.QueueLength != 5 || s.Format != "FLAC 44.1/16" || !s.Local {
		t.Fatalf("state: %+v", s)
	}
	_ = p.Seek(2 * time.Second) // a resume seek right after the start
	waitFor(t, ch, "resume seek in b", func(s player.State) bool {
		return s.Track != nil && s.Track.ID == "b" && s.Position >= 2*time.Second
	})
	_ = p.Next() // into the cue sheet
	waitFor(t, ch, "cue track One", func(s player.State) bool { return s.Track != nil && s.Track.ID == "c1" && !s.Loading })
	_ = p.Next() // next chapter of the same sheet
	s = waitFor(t, ch, "cue track Two", func(s player.State) bool {
		return s.Track != nil && s.Track.ID == "c2" && s.Position < 2*time.Second
	})
	if s.QueueIndex != 3 {
		t.Fatalf("queue index %d, want 3", s.QueueIndex)
	}
	_ = p.Seek(5 * time.Second) // position relative to the cue track
	waitFor(t, ch, "seek inside Two", func(s player.State) bool {
		return s.Track != nil && s.Track.ID == "c2" && s.Position >= 5*time.Second && s.Position < 7*time.Second
	})
	_ = p.Previous() // 5 s into Two: restart Two
	waitFor(t, ch, "restart Two", func(s player.State) bool {
		return s.Track != nil && s.Track.ID == "c2" && s.Position < time.Second
	})
	_ = p.Previous() // near the start: the previous chapter
	waitFor(t, ch, "previous chapter One", func(s player.State) bool {
		return s.Track != nil && s.Track.ID == "c1" && s.Position < 2*time.Second
	})
	_ = p.SetVolume(0.5)
	waitFor(t, ch, "volume", func(s player.State) bool { return s.Volume > 0.49 && s.Volume < 0.51 })

	// Start in the middle of a cue sheet.
	if err := p.PlayTracks([]string{"c1", "c2", "c3"}, 2); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, "start at Three", func(s player.State) bool {
		return s.Track != nil && s.Track.ID == "c3" && s.Position < 3*time.Second
	})
	_ = p.Stop()
	waitFor(t, ch, "stopped", func(s player.State) bool { return s.Track == nil })
}
