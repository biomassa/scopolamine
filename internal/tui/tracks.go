package tui

import (
	"context"
	"fmt"
	"strings"
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
type tracksLoadMsg struct{ src, key string }

// tracksDebounce keeps a fast scroll through artists/albums from starting a
// track fetch for every row passed.
const tracksDebounce = 150 * time.Millisecond

func (m *Model) scheduleTracks(key string) tea.Cmd {
	src := m.source
	return tea.Tick(tracksDebounce, func(time.Time) tea.Msg { return tracksLoadMsg{src, key} })
}

// loadTracks fetches the tracks for key (album id or artist key), from the
// cache or, when missing, from Apple Music.
func (m *Model) loadTracks(key string) tea.Cmd {
	store, source, folders := m.deps.Store, m.source, m.folders
	var src library.AppleSource
	if source == library.SourceApple {
		src = m.deps.Src // local tracks are all in the cache
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 2*time.Minute)
		defer cancel()
		albums, err := albumsForKey(ctx, store, source, folders, key)
		if err != nil {
			return tracksMsg{src: source, key: key, err: err}
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
				switch {
				case strings.HasPrefix(a.ID, library.FolderPrefix):
					perAlbum[i], errs[i] = folderTracks(ctx, store, a)
				case src == nil:
					perAlbum[i], _, errs[i] = store.Tracks(ctx, a.ID)
				default:
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
		return tracksMsg{src: source, key: key, tracks: tracks, albums: albums, err: firstErr}
	}
}

// folderTracks lists the files of a folder album. Their AlbumID becomes the
// folder album, so that the rows and the playing marks refer to it.
func folderTracks(ctx context.Context, store *library.Store, a library.Album) ([]library.Track, error) {
	ts, err := store.FolderTracks(ctx, strings.TrimPrefix(a.ID, library.FolderPrefix))
	for i := range ts {
		ts[i].AlbumID = a.ID
	}
	return ts, err
}

func albumsForKey(ctx context.Context, store *library.Store, source string, folders bool, key string) ([]library.Album, error) {
	if artist, ok := strings.CutPrefix(key, artistKeyPrefix); ok {
		if folders {
			return store.FolderAlbums(ctx, artist)
		}
		return store.AlbumsByArtist(ctx, source, artist)
	}
	if folder, ok := strings.CutPrefix(key, library.FolderPrefix); ok {
		all, err := store.FolderAlbums(ctx, "")
		for _, a := range all {
			if strings.TrimPrefix(a.ID, library.FolderPrefix) == folder {
				return []library.Album{a}, err
			}
		}
		return nil, fmt.Errorf("folder %s is gone", folder)
	}
	a, err := store.Album(ctx, key)
	if err != nil {
		return nil, err
	}
	return []library.Album{a}, nil
}
