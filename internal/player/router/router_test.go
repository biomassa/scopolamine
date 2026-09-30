package router

import (
	"sync"
	"testing"
	"time"

	"github.com/biomassa/scopolamine/internal/player"
)

type fake struct {
	mu      sync.Mutex
	calls   []string
	volume  float64
	volumes []float64 // every SetVolume
	bc      player.Broadcast
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
func (f *fake) SetVolume(v float64) error {
	f.mu.Lock()
	f.volume, f.volumes = v, append(f.volumes, v)
	f.mu.Unlock()
	return nil
}
func (f *fake) vol() float64                   { f.mu.Lock(); defer f.mu.Unlock(); return f.volume }
func (f *fake) State() player.State            { return player.State{} }
func (f *fake) Subscribe() <-chan player.State { return f.bc.Subscribe() }
func (f *fake) Close() error                   { f.bc.Close(); return nil }

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

// eventually waits up to 2 seconds for cond.
func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not met")
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
	_ = r.Toggle() // nothing plays yet: play, with a fade-in
	eventually(t, func() bool { return local.has("play") })

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

	// Starting Apple stops local (after the fade-out); the volume is shared.
	if err := r.PlayTracks([]string{"i.abc"}, 0); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return local.has("stop") && r.Active() == Apple })
	local.bc.Send(player.State{})
	eventually(t, func() bool { return local.vol() == 0.6 })
	_ = r.SetVolume(0.3)
	if apple.volume != 0.3 || local.volume != 0.3 {
		t.Fatal("volume not shared")
	}
	_ = r.Next()
	eventually(t, func() bool { return apple.has("next") })
	if local.has("next") {
		t.Fatal("next routed to local")
	}
	_ = r.Close()
}

func TestDetach(t *testing.T) {
	r := New(0.6)
	apple := &fake{}
	ch := r.Subscribe()
	r.Attach(Apple, apple)
	if err := r.PlayTracks([]string{"i.abc"}, 0); err != nil {
		t.Fatal(err)
	}
	apple.bc.Send(player.State{Track: &player.NowPlaying{ID: "i.abc"}})
	recv(t, ch)

	// Detaching the active engine: nothing plays, and the state says so.
	if p := r.Detach(Apple); p != apple {
		t.Fatalf("Detach = %v", p)
	}
	if s := recv(t, ch); s.Track != nil || s.QueueIndex != -1 || s.Volume != 0.6 {
		t.Fatalf("state after detach: %+v", s)
	}
	if r.Active() != "" || r.Has(Apple) {
		t.Fatal("engine still attached")
	}
	if err := r.PlayTracks([]string{"i.abc"}, 0); err != ErrNotStarted {
		t.Fatalf("err = %v", err)
	}
	if r.Detach(Apple) != nil {
		t.Fatal("second detach returned an engine")
	}

	// A new engine under the same name stays when the old one closes late.
	apple2 := &fake{}
	r.Attach(Apple, apple2)
	apple.bc.Send(player.State{Track: &player.NowPlaying{ID: "i.old"}, Error: "old"})
	_ = apple.Close()
	time.Sleep(50 * time.Millisecond)
	if !r.Has(Apple) {
		t.Fatal("old engine's close removed the new engine")
	}
	select {
	case s := <-ch:
		t.Fatalf("detached engine's state forwarded: %+v", s)
	default:
	}
	_ = r.Close()
}

// playingRouter returns a router with one engine that plays track id.
func playingRouter(t *testing.T, id string) (*Router, *fake) {
	t.Helper()
	r := New(0.6)
	e := &fake{}
	r.Attach(Local, e)
	if err := r.PlayTracks([]string{id}, 0); err != nil { // nothing plays: no fade
		t.Fatal(err)
	}
	e.bc.Send(player.State{Playing: true, Position: 30 * time.Second, Volume: 0.6, Track: &player.NowPlaying{ID: id}})
	eventually(t, func() bool { return r.State().Playing })
	return r, e
}

func TestFadeOut(t *testing.T) {
	r, e := playingRouter(t, "file:/a")
	defer r.Close()

	start := time.Now()
	if err := r.Stop(); err != nil || e.has("stop") {
		t.Fatalf("stop ran before the fade: %v", err)
	}
	eventually(t, func() bool { return e.has("stop") })
	if d := time.Since(start); d < fadeTime-50*time.Millisecond {
		t.Fatalf("fade took %v", d)
	}
	e.mu.Lock()
	vols := append([]float64(nil), e.volumes...)
	e.mu.Unlock()
	for i := 2; i < len(vols); i++ { // after the attach and the first step
		if vols[i] > vols[i-1] {
			t.Fatalf("fade-out not monotonic: %v", vols)
		}
	}
	if len(vols) < 6 || vols[len(vols)-1] != 0 {
		t.Fatalf("fade steps: %v", vols)
	}

	// The volume stays down until the engine reports the stop.
	time.Sleep(50 * time.Millisecond)
	if e.vol() != 0 {
		t.Fatal("volume back before the stop showed")
	}
	e.bc.Send(player.State{})
	eventually(t, func() bool { return e.vol() == 0.6 })
}

func TestFadeSecondPush(t *testing.T) {
	r, e := playingRouter(t, "file:/a")
	defer r.Close()

	_ = r.Next()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	_ = r.Next() // during the fade: both switches now
	eventually(t, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		n := 0
		for _, c := range e.calls {
			if c == "next" {
				n++
			}
		}
		return n == 2
	})
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("second push waited %v", d)
	}
	// A new track: the volume comes back.
	e.bc.Send(player.State{Playing: true, Track: &player.NowPlaying{ID: "file:/c"}})
	eventually(t, func() bool { return e.vol() == 0.6 })
}

func TestFadeIn(t *testing.T) {
	r, e := playingRouter(t, "file:/a")
	defer r.Close()

	start := time.Now()
	_ = r.Toggle() // plays: pause with a fade-out
	eventually(t, func() bool { return e.has("pause") })
	if d := time.Since(start); d < fadeTime-50*time.Millisecond {
		t.Fatalf("pause fade took %v", d)
	}
	e.bc.Send(player.State{Position: 31 * time.Second, Track: &player.NowPlaying{ID: "file:/a"}})
	eventually(t, func() bool { return e.vol() == 0.6 && !r.State().Playing })

	_ = r.Toggle() // paused: play with a fade-in
	eventually(t, func() bool { return e.has("play") })
	if e.vol() != 0 {
		t.Fatalf("fade-in starts at %v", e.vol())
	}
	e.bc.Send(player.State{Playing: true, Track: &player.NowPlaying{ID: "file:/a"}})
	time.Sleep(fadeTime / 2)
	if v := e.vol(); v <= 0 || v >= 0.6 {
		t.Fatalf("halfway volume %v", v)
	}
	eventually(t, func() bool { return e.vol() == 0.6 })
}

func TestFadeVolume(t *testing.T) {
	r, e := playingRouter(t, "file:/a")
	defer r.Close()
	ch := r.Subscribe()

	// The state shows the router's volume, not the fade's.
	e.bc.Send(player.State{Playing: true, Volume: 0.1, Track: &player.NowPlaying{ID: "file:/a"}})
	if s := recv(t, ch); s.Volume != 0.6 {
		t.Fatalf("state volume %v", s.Volume)
	}

	// A volume change during the fade: the fade does not jump, and the end
	// uses the new volume.
	_ = r.Stop()
	time.Sleep(100 * time.Millisecond)
	_ = r.SetVolume(0.9)
	if e.vol() > 0.6 {
		t.Fatalf("volume jumped to %v during the fade", e.vol())
	}
	eventually(t, func() bool { return e.has("stop") })
	e.bc.Send(player.State{})
	eventually(t, func() bool { return e.vol() == 0.9 })
}

// noisy is a fake that sends a state on each volume change, as the real
// engines do.
type noisy struct {
	fake
	id string
}

func (n *noisy) SetVolume(v float64) error {
	_ = n.fake.SetVolume(v)
	n.bc.Send(player.State{Playing: true, Position: 30 * time.Second, Track: &player.NowPlaying{ID: n.id}})
	return nil
}

// Next reports the new track at once.
func (n *noisy) Next() error {
	_ = n.log("next")
	n.bc.Send(player.State{Playing: true, Track: &player.NowPlaying{ID: "file:/b"}})
	return nil
}

func TestFadeNoisyEngine(t *testing.T) {
	r := New(0.6)
	defer r.Close()
	e := &noisy{id: "file:/a"}
	r.Attach(Local, e)
	_ = r.PlayTracks([]string{"file:/a"}, 0)
	e.bc.Send(player.State{Playing: true, Position: 30 * time.Second, Track: &player.NowPlaying{ID: "file:/a"}})
	eventually(t, func() bool { return r.State().Playing })

	_ = r.Next()
	eventually(t, func() bool { return e.has("next") })
	start := time.Now()
	eventually(t, func() bool { return e.vol() == 0.6 })
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("volume came back after %v, not at the new track", d)
	}
}

func TestFadeAway(t *testing.T) {
	r, e := playingRouter(t, "file:/a")
	defer r.Close()

	start := time.Now()
	r.FadeAway()
	if d := time.Since(start); d < fadeTime-20*time.Millisecond {
		t.Fatalf("FadeAway returned after %v", d)
	}
	if e.vol() != 0 || e.has("stop") || e.has("pause") {
		t.Fatalf("volume %v, calls %v", e.vol(), e.calls)
	}
	// The saved volume is the user's; nothing sets the engine back.
	_ = r.SetVolume(0.8)
	e.bc.Send(player.State{})
	time.Sleep(50 * time.Millisecond)
	if e.vol() != 0 || r.currentVolume() != 0.8 {
		t.Fatalf("engine %v, router %v", e.vol(), r.currentVolume())
	}
}
