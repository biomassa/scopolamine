package router

import (
	"sync"
	"testing"
	"time"

	"github.com/biomassa/scopolamine/internal/player"
)

type fake struct {
	mu     sync.Mutex
	calls  []string
	volume float64
	bc     player.Broadcast
}

func (f *fake) log(c string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return nil
}
func (f *fake) has(c string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, x := range f.calls {
		if x == c {
			return true
		}
	}
	return false
}
func (f *fake) PlayTracks(ids []string, _ int) error { return f.log("play " + ids[0]) }
func (f *fake) Play() error                          { return f.log("play") }
func (f *fake) Pause() error                         { return f.log("pause") }
func (f *fake) Toggle() error                        { return f.log("toggle") }
func (f *fake) Stop() error                          { return f.log("stop") }
func (f *fake) Next() error                          { return f.log("next") }
func (f *fake) Previous() error                      { return f.log("prev") }
func (f *fake) Seek(time.Duration) error             { return f.log("seek") }
func (f *fake) SetVolume(v float64) error            { f.mu.Lock(); f.volume = v; f.mu.Unlock(); return nil }
func (f *fake) State() player.State                  { return player.State{} }
func (f *fake) Subscribe() <-chan player.State       { return f.bc.Subscribe() }
func (f *fake) Close() error                         { f.bc.Close(); return nil }

func recv(t *testing.T, ch <-chan player.State) player.State {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(time.Second):
		t.Fatal("no state")
	}
	return player.State{}
}

func TestRouter(t *testing.T) {
	r := New(0.6)
	apple, local := &fake{}, &fake{}
	ch := r.Subscribe()
	r.Attach(Local, local)
	if local.volume != 0.6 {
		t.Fatal("volume not applied on attach")
	}
	// Apple is not started yet (it starts on the first switch).
	if err := r.PlayTracks([]string{"i.abc"}, 0); err != ErrNotStarted {
		t.Fatalf("err = %v", err)
	}
	if err := r.PlayTracks([]string{"file:/m/a.flac", "file:/m/b.flac"}, 1); err != nil || !local.has("play file:/m/a.flac") || r.Active() != Local {
		t.Fatalf("local play: %v %v", err, local.calls)
	}
	_ = r.Toggle()
	if !local.has("toggle") {
		t.Fatal("toggle not routed")
	}

	local.bc.Send(player.State{Playing: true, Track: &player.NowPlaying{ID: "file:/m/b.flac"}})
	if s := recv(t, ch); !s.Local || s.Track.ID != "file:/m/b.flac" {
		t.Fatalf("local state: %+v", s)
	}

	r.Attach(Apple, apple)
	// A state of the inactive engine is not forwarded; its notices are,
	// on top of the active state.
	apple.bc.Send(player.State{Track: &player.NowPlaying{ID: "i.x"}})
	apple.bc.Send(player.State{Error: "musickit: oops"})
	if s := recv(t, ch); s.Error != "musickit: oops" || s.Track == nil || s.Track.ID != "file:/m/b.flac" {
		t.Fatalf("notice: %+v", s)
	}

	// Starting Apple stops local; the volume is shared.
	if err := r.PlayTracks([]string{"i.abc"}, 0); err != nil || !local.has("stop") || r.Active() != Apple {
		t.Fatalf("switch to apple: %v %v", err, local.calls)
	}
	_ = r.SetVolume(0.3)
	if apple.volume != 0.3 || local.volume != 0.3 {
		t.Fatal("volume not shared")
	}
	_ = r.Next()
	if !apple.has("next") || local.has("next") {
		t.Fatal("next not routed to apple")
	}
	_ = r.Close()
}
