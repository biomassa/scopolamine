package applemusic

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CatalogArtist is an artist in the Apple Music catalog.
type CatalogArtist struct {
	ID   string
	Name string
}

// CatalogAlbum is a catalog album. Album.ID is the catalog id; LibraryID is
// the user's library copy ("l.…"), empty when the album is not in the
// library.
type CatalogAlbum struct {
	Album
	LibraryID string
	Single    bool
}

// SearchResult is a catalog search.
type SearchResult struct {
	Artists []CatalogArtist
	Albums  []CatalogAlbum
}

type catalogAlbumResource struct {
	albumResource
	Attributes struct {
		Name        string   `json:"name"`
		ArtistName  string   `json:"artistName"`
		ReleaseDate string   `json:"releaseDate"`
		TrackCount  int      `json:"trackCount"`
		GenreNames  []string `json:"genreNames"`
		Artwork     artwork  `json:"artwork"`
		IsSingle    bool     `json:"isSingle"`
	} `json:"attributes"`
	Relationships struct {
		Library struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		} `json:"library"`
	} `json:"relationships"`
}

func toCatalogAlbum(r catalogAlbumResource) CatalogAlbum {
	a := CatalogAlbum{
		Album: Album{
			ID:          r.ID,
			CatalogID:   r.ID,
			Title:       r.Attributes.Name,
			Artist:      r.Attributes.ArtistName,
			ReleaseDate: r.Attributes.ReleaseDate,
			TrackCount:  r.Attributes.TrackCount,
			ArtworkURL:  artworkURL(r.Attributes.Artwork, 600),
		},
		Single: r.Attributes.IsSingle,
	}
	if len(r.Attributes.GenreNames) > 0 {
		a.Genre = r.Attributes.GenreNames[0]
	}
	if lib := r.Relationships.Library.Data; len(lib) > 0 {
		a.LibraryID = lib[0].ID
	}
	return a
}

// storefront returns the account storefront, asking Apple once.
func (c *Client) storefront(ctx context.Context) (string, error) {
	c.sfMu.Lock()
	defer c.sfMu.Unlock()
	if c.sf != "" {
		return c.sf, nil
	}
	sf, err := c.Storefront(ctx)
	if err != nil {
		return "", err
	}
	c.sf = sf
	return sf, nil
}

// Search searches the catalog of the user's storefront for artists and
// albums. Albums come with their library relationship, so LibraryID tells
// which are already in the library.
func (c *Client) Search(ctx context.Context, term string) (SearchResult, error) {
	sf, err := c.storefront(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	var resp struct {
		Results struct {
			Artists struct {
				Data []struct {
					ID         string `json:"id"`
					Attributes struct {
						Name string `json:"name"`
					} `json:"attributes"`
				} `json:"data"`
			} `json:"artists"`
			Albums struct {
				Data []catalogAlbumResource `json:"data"`
			} `json:"albums"`
		} `json:"results"`
	}
	q := url.Values{
		"term":           {term},
		"types":          {"artists,albums"},
		"limit":          {"25"},
		"relate[albums]": {"library"},
	}
	if err := c.get(ctx, "/catalog/"+sf+"/search?"+q.Encode(), &resp); err != nil {
		return SearchResult{}, err
	}
	var out SearchResult
	for _, a := range resp.Results.Artists.Data {
		out.Artists = append(out.Artists, CatalogArtist{ID: a.ID, Name: a.Attributes.Name})
	}
	for _, a := range resp.Results.Albums.Data {
		out.Albums = append(out.Albums, toCatalogAlbum(a))
	}
	return out, nil
}

// ArtistAlbums returns an artist's catalog discography, with library
// relationships filled in.
func (c *Client) ArtistAlbums(ctx context.Context, artistID string) ([]CatalogAlbum, error) {
	sf, err := c.storefront(ctx)
	if err != nil {
		return nil, err
	}
	var albums []CatalogAlbum
	err = paginate(ctx, c, "/catalog/"+sf+"/artists/"+url.PathEscape(artistID)+"/albums?limit=100", func(r catalogAlbumResource) {
		albums = append(albums, toCatalogAlbum(r))
	})
	if err != nil {
		return nil, err
	}
	// The relationship endpoint cannot relate=library; look it up in batches.
	for i := 0; i < len(albums); i += 100 {
		batch := albums[i:min(i+100, len(albums))]
		ids := make([]string, len(batch))
		for j, a := range batch {
			ids[j] = a.ID
		}
		lib, err := c.libraryIDs(ctx, sf, ids)
		if err != nil {
			return albums, nil //nolint:nilerr // the marks are a nicety; the list is what matters
		}
		for j := range batch {
			batch[j].LibraryID = lib[batch[j].ID]
		}
	}
	return albums, nil
}

// libraryIDs maps catalog album ids to the user's library album ids.
func (c *Client) libraryIDs(ctx context.Context, sf string, ids []string) (map[string]string, error) {
	var p page[catalogAlbumResource]
	q := url.Values{"ids": {strings.Join(ids, ",")}, "relate": {"library"}}
	if err := c.get(ctx, "/catalog/"+sf+"/albums?"+q.Encode(), &p); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(p.Data))
	for _, r := range p.Data {
		if lib := r.Relationships.Library.Data; len(lib) > 0 {
			out[r.ID] = lib[0].ID
		}
	}
	return out, nil
}

// CatalogAlbumTracks returns a catalog album's tracks. Track.ID is the
// catalog song id; Playable is false for songs without a stream.
func (c *Client) CatalogAlbumTracks(ctx context.Context, albumID string) ([]Track, error) {
	sf, err := c.storefront(ctx)
	if err != nil {
		return nil, err
	}
	var out []Track
	err = paginate(ctx, c, "/catalog/"+sf+"/albums/"+url.PathEscape(albumID)+"/tracks?limit=300", func(r songResource) {
		t := toTrack(r)
		t.CatalogID = r.ID
		out = append(out, t)
	})
	return out, err
}

// AddAlbumToLibrary adds a catalog album to the user's library. Apple
// processes the request asynchronously; see LibraryAlbumOf.
func (c *Client) AddAlbumToLibrary(ctx context.Context, catalogID string) error {
	return c.do(ctx, http.MethodPost, "/me/library?"+url.Values{"ids[albums]": {catalogID}}.Encode(), nil)
}

// ErrNotInLibrary means the catalog album has no library copy (yet).
var ErrNotInLibrary = errors.New("apple music: album not in library")

// LibraryAlbumOf returns the library copy of a catalog album.
func (c *Client) LibraryAlbumOf(ctx context.Context, catalogID string) (Album, error) {
	sf, err := c.storefront(ctx)
	if err != nil {
		return Album{}, err
	}
	lib, err := c.libraryIDs(ctx, sf, []string{catalogID})
	if err != nil {
		return Album{}, err
	}
	id := lib[catalogID]
	if id == "" {
		return Album{}, ErrNotInLibrary
	}
	var p page[albumResource]
	if err := c.get(ctx, "/me/library/albums/"+url.PathEscape(id), &p); err != nil {
		return Album{}, err
	}
	if len(p.Data) == 0 {
		return Album{}, ErrNotInLibrary
	}
	a := toAlbum(p.Data[0])
	a.CatalogID = catalogID
	return a, nil
}

// AddAlbumAndWait adds a catalog album and waits (up to ~15 s) until its
// library copy exists, returning it.
func (c *Client) AddAlbumAndWait(ctx context.Context, catalogID string) (Album, error) {
	if err := c.AddAlbumToLibrary(ctx, catalogID); err != nil {
		return Album{}, err
	}
	var last error
	for i := 0; i < 10; i++ {
		a, err := c.LibraryAlbumOf(ctx, catalogID)
		if err == nil {
			return a, nil
		}
		last = err
		select {
		case <-time.After(1500 * time.Millisecond):
		case <-ctx.Done():
			return Album{}, ctx.Err()
		}
	}
	return Album{}, last
}

// DeleteLibraryAlbum removes an album (library id "l.…") from the user's
// library, then checks that it is really gone: Apple answers 204 even for
// ids it does not know, so the answer alone proves nothing.
func (c *Client) DeleteLibraryAlbum(ctx context.Context, libraryID string) error {
	if !strings.HasPrefix(libraryID, "l.") {
		return errors.New("apple music: not a library album id: " + libraryID)
	}
	if err := c.do(ctx, http.MethodDelete, c.ampURL+"/me/library/albums/"+url.PathEscape(libraryID), nil); err != nil {
		return err
	}
	for i := 0; i < 6; i++ {
		var p page[albumResource]
		err := c.get(ctx, "/me/library/albums/"+url.PathEscape(libraryID), &p)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err == nil && len(p.Data) == 0 {
			return nil
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return errors.New("apple music: album still in library after delete")
}
