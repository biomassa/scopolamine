// Package router puts the Apple Music and the local-file players behind one
// player.Player. The TUI and MPRIS talk to the router; it forwards each
// command to the engine that plays.
package router

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/biomassa/scopolamine/internal/player"
)

// Engine names.
const (
	Apple = "apple"
	Local = "local"
)

// LocalIDPrefix starts the ids of local tracks (library.LocalTrackID).
const LocalIDPrefix = "file:"

// ErrNotStarted means the engine for the tracks is not (yet) attached.
var ErrNotStarted = errors.New("player not started")

// Router is a player.Player over the attached engines.
type Router struct {
	mu      sync.Mutex
	engines map[string]player.Player
	active  string // the engine that plays, or ""
	volume  float64
	last    player.State // the last state of the active engine
	fade    *fade        // the fade that runs, or nil

	bcast player.Broadcast
}

var _ player.Player = (*Router)(nil)

// New returns a router without engines.
func New(volume float64) *Router {
	if volume <= 0 {
		volume = 1
	}
	return &Router{engines: map[string]player.Player{}, volume: volume, last: player.State{QueueIndex: -1, Volume: volume}}
}

// Attach adds an engine under name and forwards its states.
func (r *Router) Attach(name string, p player.Player) {
	ch := p.Subscribe()
	r.mu.Lock()
	r.engines[name] = p
	v := r.volume
	r.mu.Unlock()
	_ = p.SetVolume(v)
	go r.forward(name, p, ch)
}

// Detach removes the engine name and returns it, or nil. It does not close
// the engine. If the engine was the active one, nothing plays now.
func (r *Router) Detach(name string) player.Player {
	r.mu.Lock()
	p := r.engines[name]
	delete(r.engines, name)
	wasActive := p != nil && r.active == name
	if wasActive {
		r.active = ""
		r.last = player.State{QueueIndex: -1, Volume: r.volume}
	}
	s := r.last
	r.mu.Unlock()
	if wasActive {
		r.bcast.Send(s)
	}
	return p
}

// Has reports whether the engine name is attached.
func (r *Router) Has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.engines[name] != nil
}

// Active returns the engine that plays, or "".
func (r *Router) Active() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

func (r *Router) forward(name string, p player.Player, ch <-chan player.State) {
	for s := range ch {
		s.Local = name == Local
		r.mu.Lock()
		if r.engines[name] != p { // detached
			r.mu.Unlock()
			continue
		}
		if f := r.fade; f != nil {
			f.offer(namedState{name, s})
		}
		// The volume is the router's: a fade does not show as a volume change.
		s.Volume = r.volume
		active := r.active == name
		if active {
			r.last = s
		}
		base := r.last
		r.mu.Unlock()
		switch {
		case active:
			r.bcast.Send(s)
		case s.Error != "" || s.Log != "" || s.NeedsAuth:
			// A notice of the other engine; the state stays the active one's.
			base.Error, base.Log, base.NeedsAuth, base.SkippedID = s.Error, s.Log, s.NeedsAuth, ""
			r.bcast.Send(base)
		}
	}
	r.mu.Lock()
	if r.engines[name] == p { // not detached or replaced
		delete(r.engines, name)
		if r.active == name {
			r.active = ""
		}
	}
	r.mu.Unlock()
}

// engineFor returns the engine for track ids.
func engineFor(ids []string) string {
	if len(ids) > 0 && strings.HasPrefix(ids[0], LocalIDPrefix) {
		return Local
	}
	return Apple
}

// PlayTracks plays ids on their engine. The other engine stops.
func (r *Router) PlayTracks(ids []string, start int) error {
	name := engineFor(ids)
	r.mu.Lock()
	p := r.engines[name]
	var others []player.Player
	for n, e := range r.engines {
		if n != name {
			others = append(others, e)
		}
	}
	from := r.active
	r.mu.Unlock()
	if p == nil {
		return ErrNotStarted
	}
	r.mu.Lock()
	before := r.last
	r.mu.Unlock()
	settled := changed(name, before)
	if from != name {
		settled = stopped(from)
	}
	return r.fadeOut(fadeTime, func() error {
		for _, o := range others {
			_ = o.Stop()
		}
		r.mu.Lock()
		if r.active != name { // the state of the other engine is old now
			r.active = name
			r.last = player.State{QueueIndex: -1, Volume: r.volume}
		}
		r.mu.Unlock()
		return p.PlayTracks(ids, start)
	}, settled)
}

func (r *Router) activePlayer() player.Player {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.engines[r.active]
}

func (r *Router) do(f func(player.Player) error) error {
	p := r.activePlayer()
	if p == nil {
		return nil // nothing plays: nothing to control
	}
	return f(p)
}

// Play fades in after a pause.
func (r *Router) Play() error {
	return r.fadeIn(func() error { return r.do(player.Player.Play) })
}

// Pause, Stop, Next, and Previous fade out first.
func (r *Router) Pause() error {
	return r.fadeOutActive(fadeTime, player.Player.Pause, stopped)
}

func (r *Router) Stop() error { return r.fadeOutActive(fadeTime, player.Player.Stop, stopped) }

func (r *Router) Next() error {
	return r.fadeOutActive(fadeTime, player.Player.Next, r.changedActive)
}

func (r *Router) Previous() error {
	return r.fadeOutActive(fadeTime, player.Player.Previous, r.changedActive)
}

// Toggle pauses with a fade-out, or plays with a fade-in.
func (r *Router) Toggle() error {
	r.mu.Lock()
	playing := r.last.Playing
	if f := r.fade; f != nil && f.fadesIn {
		playing = true // a fade-in runs: the music plays again
	}
	r.mu.Unlock()
	if playing {
		return r.Pause()
	}
	return r.Play()
}

// fadeOutActive runs op on the active engine after a fade-out. settled
// gets the engine name and tells when the result of op shows.
func (r *Router) fadeOutActive(length time.Duration, op func(player.Player) error, settled func(string) func(namedState) bool) error {
	r.mu.Lock()
	name := r.active
	r.mu.Unlock()
	return r.fadeOut(length, func() error { return r.do(op) }, settled(name))
}

func (r *Router) changedActive(name string) func(namedState) bool {
	r.mu.Lock()
	before := r.last
	r.mu.Unlock()
	return changed(name, before)
}

func (r *Router) Seek(pos time.Duration) error {
	return r.do(func(p player.Player) error { return p.Seek(pos) })
}

// SetVolume sets the one volume of both engines.
func (r *Router) SetVolume(v float64) error {
	r.mu.Lock()
	r.volume = v
	engines := make([]player.Player, 0, len(r.engines))
	for _, e := range r.engines {
		if f := r.fade; f != nil && f.engine == e {
			continue // the fade uses the new volume
		}
		engines = append(engines, e)
	}
	r.mu.Unlock()
	for _, e := range engines {
		_ = e.SetVolume(v)
	}
	return nil
}

// State is the state of the engine that plays.
func (r *Router) State() player.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

func (r *Router) Subscribe() <-chan player.State { return r.bcast.Subscribe() }

// Close closes all engines.
func (r *Router) Close() error {
	r.takeFade(nil)
	r.mu.Lock()
	engines := make([]player.Player, 0, len(r.engines))
	for _, e := range r.engines {
		engines = append(engines, e)
	}
	r.mu.Unlock()
	for _, e := range engines {
		_ = e.Close()
	}
	r.bcast.Close()
	return nil
}
