package library

import (
	"context"

	"github.com/biomassa/scopolamine/internal/applemusic"
)

// AppleSource is what the syncer needs from the Apple Music client.
type AppleSource interface {
	LibraryAlbums(ctx context.Context, progress func(n, total int)) ([]applemusic.Album, error)
	AlbumTracks(ctx context.Context, albumID string) ([]applemusic.Track, error)
}

// SyncAppleAlbums refreshes the album list from Apple Music. Track lists are
// fetched lazily per album by AppleTracks.
func SyncAppleAlbums(ctx context.Context, s *Store, src AppleSource, progress func(n, total int)) (int, error) {
	remote, err := src.LibraryAlbums(ctx, progress)
	if err != nil {
		return 0, err
	}
	albums := make([]Album, 0, len(remote))
	for _, r := range remote {
		albums = append(albums, FromApple(r))
	}
	if err := s.ReplaceAlbums(ctx, SourceApple, albums); err != nil {
		return 0, err
	}
	return len(albums), nil
}

// AppleTracks returns an album's tracks from the cache, fetching and caching
// them from Apple Music first when they have not been synced.
func AppleTracks(ctx context.Context, s *Store, src AppleSource, albumID string) ([]Track, error) {
	tracks, synced, err := s.Tracks(ctx, albumID)
	if err != nil {
		return nil, err
	}
	if synced {
		return tracks, nil
	}
	remote, err := src.AlbumTracks(ctx, albumID)
	if err != nil {
		return nil, err
	}
	tracks = make([]Track, 0, len(remote))
	for _, r := range remote {
		tracks = append(tracks, Track{
			ID:        r.ID,
			AlbumID:   albumID,
			CatalogID: r.CatalogID,
			Title:     r.Title,
			Artist:    r.Artist,
			Disc:      r.Disc,
			Number:    r.Number,
			Duration:  r.Duration,
			Playable:  r.Playable,
		})
	}
	if err := s.SetTracks(ctx, albumID, tracks); err != nil {
		return nil, err
	}
	tracks, _, err = s.Tracks(ctx, albumID)
	return tracks, err
}

// FromApple converts an Apple Music library album.
func FromApple(r applemusic.Album) Album {
	return Album{
		ID:          r.ID,
		CatalogID:   r.CatalogID,
		Title:       r.Title,
		Artist:      r.Artist,
		ReleaseDate: r.ReleaseDate,
		DateAdded:   r.DateAdded,
		TrackCount:  r.TrackCount,
		Genre:       r.Genre,
		ArtworkURL:  r.ArtworkURL,
	}
}
