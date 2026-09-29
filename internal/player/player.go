// Package player defines the playback engine interface. The Apple Music
// engine (package cdp) plays through headless Chrome; a local-file engine
// (libmpv) will implement the same interface later.
package player

import (
	"sync"
	"time"
)

// NowPlaying describes the current item.
type NowPlaying struct {
	ID         string
	CatalogID  string
	Title      string
	Artist     string
	Album      string
	ArtworkURL string
	Duration   time.Duration
}

// State is a snapshot of the engine.
type State struct {
	Ready       bool
	Playing     bool
	Loading     bool
	Position    time.Duration
	Volume      float64 // 0..1
	BitrateKbps int     // 0 = unknown
	QueueIndex  int     // -1 when idle
	QueueLength int
	Track       *NowPlaying
	Storefront  string

	// Transient notices, set on the one State they belong to.
	Error     string
	Log       string
	SkippedID string
	NeedsAuth bool
}

// Player controls playback of an ordered list of tracks (normally an album).
type Player interface {
	// PlayTracks replaces the queue with ids and starts at ids[start].
	PlayTracks(ids []string, start int) error
	Play() error
	Pause() error
	Toggle() error
	Stop() error
	Next() error
	Previous() error
	Seek(position time.Duration) error
	SetVolume(v float64) error
	State() State
	Subscribe() <-chan State
	Close() error
}

// Broadcast fans State out to subscribers without ever blocking the sender:
// a slow subscriber only misses intermediate snapshots.
type Broadcast struct {
	mu   sync.Mutex
	subs []chan State
}

// Subscribe returns a channel receiving every subsequent State.
func (b *Broadcast) Subscribe() <-chan State {
	ch := make(chan State, 16)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

// Send delivers s to every subscriber, dropping it for full ones. Notices
// (Error/Log/SkippedID/NeedsAuth) are never dropped silently for a full
// channel: the oldest queued value is discarded instead.
func (b *Broadcast) Send(s State) {
	notice := s.Error != "" || s.Log != "" || s.SkippedID != "" || s.NeedsAuth
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- s:
			continue
		default:
		}
		if !notice {
			continue
		}
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- s:
		default:
		}
	}
}

// Close closes every subscriber channel.
func (b *Broadcast) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		close(ch)
	}
	b.subs = nil
}
