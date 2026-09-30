package router

import (
	"sync"
	"time"

	"github.com/biomassa/scopolamine/internal/player"
)

// Fades: a stop, a pause, and a change of track fade the music out first.
// Play after a pause fades it in. The engines do not know about fades: the
// router steps their volume.
const (
	fadeTime  = 200 * time.Millisecond
	fadeStep  = 25 * time.Millisecond
	settleMax = 2 * time.Second // the longest wait for the result of the command
)

// namedState is a state with the name of its engine.
type namedState struct {
	name string
	s    player.State
}

// fade is one faded command. Only one fade runs; a new command ends it.
type fade struct {
	engine  player.Player
	name    string // the engine name
	states  chan namedState
	length  time.Duration
	quick   bool // no fade-out: the command runs at once
	fadesIn bool

	endOnce sync.Once
	end     chan struct{} // closed: a new command ends this fade
	ran     chan struct{} // closed: the command has run
}

func newFade(name string, p player.Player) *fade {
	return &fade{engine: p, name: name, states: make(chan namedState, 16),
		end: make(chan struct{}), ran: make(chan struct{})}
}

func (f *fade) stop() { f.endOnce.Do(func() { close(f.end) }) }

// offer queues a state for the fade. When the queue is full, the oldest
// state goes, so that the newest state always arrives.
func (f *fade) offer(ns namedState) {
	for {
		select {
		case f.states <- ns:
			return
		default:
		}
		select {
		case <-f.states:
		default:
		}
	}
}

// drain removes the queued states.
func (f *fade) drain() {
	for {
		select {
		case <-f.states:
		default:
			return
		}
	}
}

// takeFade ends the fade that runs, if any. Its command runs at once, and
// the caller then owns the volume. It reports whether a fade ran.
func (r *Router) takeFade(next *fade) bool {
	r.mu.Lock()
	prev := r.fade
	r.fade = next
	r.mu.Unlock()
	if prev == nil {
		return false
	}
	prev.stop()
	<-prev.ran
	return true
}

// fadeOut runs op after a fade-out of the active engine. The volume stays
// down until settled accepts a state, so that the old music does not
// return at full volume. If nothing plays, op runs at once without a fade.
// After a fade that a new command ends, op runs at once.
func (r *Router) fadeOut(length time.Duration, op func() error, settled func(namedState) bool) error {
	r.mu.Lock()
	name := r.active
	p := r.engines[name]
	playing := p != nil && r.last.Playing
	busy := r.fade != nil
	r.mu.Unlock()
	if p == nil || (!playing && !busy) {
		return op()
	}
	f := newFade(name, p)
	f.length = length
	f.quick = busy // the second push: no new fade
	r.takeFade(f)
	go r.runFadeOut(f, op, settled)
	return nil
}

func (r *Router) runFadeOut(f *fade, op func() error, settled func(namedState) bool) {
	start := time.Now()
	tick := time.NewTicker(fadeStep)
	defer tick.Stop()
ramp:
	for !f.quick {
		k := float64(time.Since(start)) / float64(f.length)
		if k >= 1 {
			break
		}
		_ = f.engine.SetVolume(r.currentVolume() * (1 - k) * (1 - k))
		select {
		case <-f.end:
			break ramp
		case <-tick.C:
		}
	}
	_ = f.engine.SetVolume(0)
	f.drain() // only a state after the command tells its result
	err := op()
	close(f.ran)
	if err != nil {
		r.notice(err)
	}
	timeout := time.After(settleMax)
	for {
		select {
		case ns := <-f.states:
			if !settled(ns) {
				continue
			}
		case <-timeout:
		case <-f.end:
			return // a new command owns the volume now
		}
		break
	}
	r.endFade(f)
}

// fadeIn runs op (play) and then fades the music in. If the music plays
// already, op runs at once.
func (r *Router) fadeIn(op func() error) error {
	r.mu.Lock()
	name := r.active
	p := r.engines[name]
	playing := p != nil && r.last.Playing
	busy := r.fade != nil
	r.mu.Unlock()
	if p == nil || (playing && !busy) {
		return op()
	}
	f := newFade(name, p)
	f.length = fadeTime
	f.fadesIn = true
	r.takeFade(f)
	_ = p.SetVolume(0)
	go r.runFadeIn(f, op)
	return nil
}

func (r *Router) runFadeIn(f *fade, op func() error) {
	f.drain()
	err := op()
	close(f.ran)
	if err != nil {
		r.notice(err)
		r.endFade(f)
		return
	}
	// The ramp starts when the music plays, so that it is not over before
	// the engine has started.
	timeout := time.After(settleMax)
wait:
	for {
		select {
		case ns := <-f.states:
			if ns.name == f.name && ns.s.Playing {
				break wait
			}
		case <-timeout:
			break wait
		case <-f.end:
			return
		}
	}
	start := time.Now()
	tick := time.NewTicker(fadeStep)
	defer tick.Stop()
	for {
		k := float64(time.Since(start)) / float64(f.length)
		if k >= 1 {
			break
		}
		_ = f.engine.SetVolume(r.currentVolume() * k * k)
		select {
		case <-f.end:
			return
		case <-tick.C:
		}
	}
	r.endFade(f)
}

// endFade sets the volume of all engines back, if f is still the fade that
// runs.
func (r *Router) endFade(f *fade) {
	r.mu.Lock()
	if r.fade != f {
		r.mu.Unlock()
		return
	}
	r.fade = nil
	v := r.volume
	engines := make([]player.Player, 0, len(r.engines))
	for _, e := range r.engines {
		engines = append(engines, e)
	}
	r.mu.Unlock()
	for _, e := range engines {
		_ = e.SetVolume(v)
	}
}

func (r *Router) currentVolume() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.volume
}

// notice sends err on top of the state that shows.
func (r *Router) notice(err error) {
	r.mu.Lock()
	s := r.last
	r.mu.Unlock()
	s.Error, s.Log, s.NeedsAuth, s.SkippedID = err.Error(), "", false, ""
	r.bcast.Send(s)
}

// stopped accepts a state of the faded engine that does not play.
func stopped(name string) func(namedState) bool {
	return func(ns namedState) bool { return ns.name == name && !ns.s.Playing }
}

// changed accepts a state of engine name with a different track, or with
// the same track from an earlier position (previous restarts the track).
func changed(name string, before player.State) func(namedState) bool {
	return func(ns namedState) bool {
		if ns.name != name {
			return false
		}
		s := ns.s
		switch {
		case s.Track == nil || before.Track == nil:
			return true
		case s.Track.ID != before.Track.ID:
			return true
		default:
			return s.Position+time.Second < before.Position
		}
	}
}

// FadeAway lowers the volume of the music that plays to 0 in fadeTime and
// returns then. It is for the quit: it does not stop the music, so that the
// track and position stay, and it does not set the volume back.
func (r *Router) FadeAway() {
	r.mu.Lock()
	name := r.active
	p := r.engines[name]
	playing := p != nil && r.last.Playing
	r.mu.Unlock()
	if p == nil {
		return
	}
	f := newFade(name, p)
	close(f.ran)
	r.takeFade(f) // no volume changes from here on
	if !playing {
		return
	}
	start := time.Now()
	for {
		k := float64(time.Since(start)) / float64(fadeTime)
		if k >= 1 {
			break
		}
		_ = p.SetVolume(r.currentVolume() * (1 - k) * (1 - k))
		time.Sleep(fadeStep)
	}
	_ = p.SetVolume(0)
}
