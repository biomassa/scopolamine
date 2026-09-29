package tui

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/library"
)

// Tracks-pane content is identified by a key: a library album id, or
// artistKeyPrefix+artist for "All albums" of one artist.
const artistKeyPrefix = "artist:"

// sessionAllAlbums is the Session.AlbumID recorded when "All albums" was
// selected.
const sessionAllAlbums = "*all*"

type rowKind int

const (
	rowAll rowKind = iota
	rowHeader
	rowTrack
)

// trackRow is one line of the tracks pane.
type trackRow struct {
	kind  rowKind
	album library.Album // rowHeader, rowTrack
	track int           // rowTrack: index into Model.tracks
}

// buildTrackRows lays tracks out under an "All" row, with a header per album
// when grouped.
func buildTrackRows(tracks []library.Track, albums []library.Album, grouped bool) []trackRow {
	byID := make(map[string]library.Album, len(albums))
	for _, a := range albums {
		byID[a.ID] = a
	}
	rows := []trackRow{{kind: rowAll}}
	last := ""
	for i, t := range tracks {
		a := byID[t.AlbumID]
		if grouped && t.AlbumID != last {
			rows = append(rows, trackRow{kind: rowHeader, album: a})
			last = t.AlbumID
		}
		rows = append(rows, trackRow{kind: rowTrack, album: a, track: i})
	}
	return rows
}

func (m *Model) trackRowLabels() []string {
	labels := make([]string, len(m.trackRows))
	for i, r := range m.trackRows {
		switch r.kind {
		case rowAll:
			labels[i] = "All"
		case rowHeader:
			labels[i] = r.album.Title + " " + r.album.Artist
		case rowTrack:
			t := m.tracks[r.track]
			labels[i] = t.Title + " " + t.Artist
		}
	}
	return labels
}

// tracksLoadMsg fires after the cursor has rested on key for a moment.
type tracksLoadMsg struct{ key string }

// tracksDebounce keeps a fast scroll through artists/albums from starting a
// track fetch for every row passed.
const tracksDebounce = 150 * time.Millisecond

func (m *Model) scheduleTracks(key string) tea.Cmd {
	return tea.Tick(tracksDebounce, func(time.Time) tea.Msg { return tracksLoadMsg{key} })
}

// loadTracks fetches the tracks for key (album id or artist key), from the
// cache or, when missing, from Apple Music.
func (m *Model) loadTracks(key string) tea.Cmd {
	store, src := m.deps.Store, m.deps.Src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 2*time.Minute)
		defer cancel()
		albums, err := albumsForKey(ctx, store, key)
		if err != nil {
			return tracksMsg{key: key, err: err}
		}
		perAlbum := make([][]library.Track, len(albums))
		errs := make([]error, len(albums))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for i, a := range albums {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if src == nil {
					perAlbum[i], _, errs[i] = store.Tracks(ctx, a.ID)
				} else {
					perAlbum[i], errs[i] = library.AppleTracks(ctx, store, src, a.ID)
				}
			}()
		}
		wg.Wait()
		var tracks []library.Track
		var firstErr error
		for i := range albums {
			if errs[i] != nil && firstErr == nil {
				firstErr = errs[i]
			}
			tracks = append(tracks, perAlbum[i]...)
		}
		if len(tracks) > 0 {
			firstErr = nil // partial results beat none for "All albums"
		}
		return tracksMsg{key: key, tracks: tracks, albums: albums, err: firstErr}
	}
}

func albumsForKey(ctx context.Context, store *library.Store, key string) ([]library.Album, error) {
	if len(key) > len(artistKeyPrefix) && key[:len(artistKeyPrefix)] == artistKeyPrefix {
		return store.AlbumsByArtist(ctx, key[len(artistKeyPrefix):])
	}
	a, err := store.Album(ctx, key)
	if err != nil {
		return nil, err
	}
	return []library.Album{a}, nil
}
