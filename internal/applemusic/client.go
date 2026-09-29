// Package applemusic is a small Apple Music API client limited to what an
// album-centric library browser needs: library albums and their tracks.
//
// The request/response plumbing is derived from vibez
// (https://github.com/simonepelosi/vibez), Copyright (c) 2025 Simone Pelosi,
// MIT License.
package applemusic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBaseURL = "https://api.music.apple.com/v1"
	// ampBaseURL is the web player's API host. Library deletes are only
	// accepted there (the public host answers 401).
	ampBaseURL = "https://amp-api.music.apple.com/v1"
)

// ErrNotFound is returned for HTTP 404.
var ErrNotFound = errors.New("apple music: not found")

// ErrUnauthorized is returned for HTTP 401/403, i.e. a bad or expired token.
var ErrUnauthorized = errors.New("apple music: not authorized")

// Client talks to the Apple Music API on behalf of one user.
type Client struct {
	DevToken  string
	UserToken string
	// Origin, when set, is sent as the Origin header. Required for developer
	// tokens restricted with a root_https_origin claim (the web player's).
	Origin string

	http    *http.Client
	baseURL string
	ampURL  string

	sfMu sync.Mutex
	sf   string // cached storefront
}

// New returns a client for the public API host.
func New(devToken, userToken string) *Client {
	return &Client{
		DevToken:  devToken,
		UserToken: userToken,
		http:      &http.Client{Timeout: 20 * time.Second},
		baseURL:   defaultBaseURL,
		ampURL:    ampBaseURL,
	}
}

// SetBaseURL points the client at another host. Tests only.
func (c *Client) SetBaseURL(u string) {
	c.baseURL = strings.TrimRight(u, "/")
	c.ampURL = c.baseURL
}

// errRateLimited marks an HTTP 429 so get can retry it.
var errRateLimited = errors.New("apple music: rate limited")

// get fetches endpoint (a path like "/me/library/albums?limit=100", or a
// "next" link such as "/v1/me/library/albums?offset=100") into dst.
// Rate-limited requests are retried with backoff.
func (c *Client) get(ctx context.Context, endpoint string, dst any) error {
	return c.do(ctx, http.MethodGet, endpoint, dst)
}

// do performs a request (GET or a body-less POST), retrying rate limits.
func (c *Client) do(ctx context.Context, method, endpoint string, dst any) error {
	delay := time.Second
	for attempt := 0; ; attempt++ {
		err := c.doOnce(ctx, method, endpoint, dst)
		if !errors.Is(err, errRateLimited) || attempt >= 5 {
			return err
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
		delay *= 2
	}
}

func (c *Client) doOnce(ctx context.Context, method, endpoint string, dst any) error {
	u := endpoint
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = c.baseURL + strings.TrimPrefix(u, "/v1")
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.DevToken)
	req.Header.Set("Music-User-Token", c.UserToken)
	if c.Origin != "" {
		req.Header.Set("Origin", c.Origin)
		req.Header.Set("Referer", c.Origin+"/")
	}

	resp, err := c.http.Do(req) //nolint:gosec // URL built from a fixed base
	if err != nil {
		return fmt.Errorf("apple music: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w (%s)", ErrUnauthorized, resp.Status)
	case resp.StatusCode == http.StatusTooManyRequests:
		return errRateLimited
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w (%s)", ErrNotFound, u)
	case resp.StatusCode >= 400:
		msg := strings.TrimSpace(string(body))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return fmt.Errorf("apple music %s: %s", resp.Status, msg)
	}
	if dst == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, dst)
}

// --- response types -------------------------------------------------------

type artwork struct {
	URL string `json:"url"`
}

// ArtworkURL expands Apple's "{w}x{h}" template to a square of size px.
func artworkURL(a artwork, size int) string {
	s := strconv.Itoa(size)
	return strings.NewReplacer("{w}", s, "{h}", s).Replace(a.URL)
}

type playParams struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	IsLibrary bool   `json:"isLibrary"`
	CatalogID string `json:"catalogId"`
}

type albumResource struct {
	ID         string `json:"id"`
	Attributes struct {
		Name        string      `json:"name"`
		ArtistName  string      `json:"artistName"`
		ReleaseDate string      `json:"releaseDate"`
		DateAdded   string      `json:"dateAdded"`
		TrackCount  int         `json:"trackCount"`
		GenreNames  []string    `json:"genreNames"`
		Artwork     artwork     `json:"artwork"`
		PlayParams  *playParams `json:"playParams"`
	} `json:"attributes"`
}

type songResource struct {
	ID         string `json:"id"`
	Attributes struct {
		Name        string      `json:"name"`
		ArtistName  string      `json:"artistName"`
		AlbumName   string      `json:"albumName"`
		DiscNumber  int         `json:"discNumber"`
		TrackNumber int         `json:"trackNumber"`
		DurationMs  int64       `json:"durationInMillis"`
		PlayParams  *playParams `json:"playParams"`
	} `json:"attributes"`
}

type page[T any] struct {
	Data []T    `json:"data"`
	Next string `json:"next"`
	Meta struct {
		Total int `json:"total"`
	} `json:"meta"`
}

// --- public model ---------------------------------------------------------

// Album is a library album.
type Album struct {
	ID          string // library id, "l.xxxx"
	CatalogID   string
	Title       string
	Artist      string // album artist as Apple reports it
	ReleaseDate string // "YYYY-MM-DD" or "YYYY" or ""
	DateAdded   string
	TrackCount  int
	Genre       string
	ArtworkURL  string // 600px
}

// Track is a library song.
type Track struct {
	ID        string // library id, "i.xxxx"; what the player queues
	CatalogID string
	Title     string
	Artist    string
	Disc      int
	Number    int
	Duration  time.Duration
	// Playable is false when Apple offers no stream (no playParams): the
	// song is not in the catalog of the user's storefront.
	Playable bool
}

func toAlbum(r albumResource) Album {
	a := Album{
		ID:          r.ID,
		Title:       r.Attributes.Name,
		Artist:      r.Attributes.ArtistName,
		ReleaseDate: r.Attributes.ReleaseDate,
		DateAdded:   r.Attributes.DateAdded,
		TrackCount:  r.Attributes.TrackCount,
		ArtworkURL:  artworkURL(r.Attributes.Artwork, 600),
	}
	if len(r.Attributes.GenreNames) > 0 {
		a.Genre = r.Attributes.GenreNames[0]
	}
	if p := r.Attributes.PlayParams; p != nil {
		a.CatalogID = p.CatalogID
	}
	return a
}

func toTrack(r songResource) Track {
	t := Track{
		ID:       r.ID,
		Title:    r.Attributes.Name,
		Artist:   r.Attributes.ArtistName,
		Disc:     r.Attributes.DiscNumber,
		Number:   r.Attributes.TrackNumber,
		Duration: time.Duration(r.Attributes.DurationMs) * time.Millisecond,
	}
	if p := r.Attributes.PlayParams; p != nil {
		t.CatalogID = p.CatalogID
		t.Playable = true
	}
	return t
}

// maxPages bounds pagination so a server that keeps returning "next" cannot
// loop forever (100 per page → 200k items).
const maxPages = 2000

func paginate[T any](ctx context.Context, c *Client, first string, each func(T)) error {
	next := first
	for i := 0; next != "" && i < maxPages; i++ {
		var p page[T]
		if err := c.get(ctx, next, &p); err != nil {
			return err
		}
		for _, item := range p.Data {
			each(item)
		}
		next = p.Next
	}
	return nil
}

// syncConcurrency is how many library pages are fetched at once.
const syncConcurrency = 8

// LibraryAlbums returns every album in the user's library. progress, if not
// nil, is called with the running count and the total (0 if unknown) as
// pages arrive.
//
// When the first page reports meta.total, the remaining pages are fetched
// concurrently by offset; otherwise it follows "next" links one by one.
func (c *Client) LibraryAlbums(ctx context.Context, progress func(n, total int)) ([]Album, error) {
	const limit = 100
	report := func(n, total int) {
		if progress != nil {
			progress(n, total)
		}
	}

	var first page[albumResource]
	if err := c.get(ctx, fmt.Sprintf("/me/library/albums?limit=%d", limit), &first); err != nil {
		return nil, err
	}
	total := first.Meta.Total
	if total <= len(first.Data) || first.Next == "" {
		// Everything fit, or no total to plan with: follow "next" serially.
		out := make([]Album, 0, len(first.Data))
		for _, r := range first.Data {
			out = append(out, toAlbum(r))
		}
		report(len(out), total)
		err := paginate(ctx, c, first.Next, func(r albumResource) {
			out = append(out, toAlbum(r))
			if len(out)%limit == 0 {
				report(len(out), total)
			}
		})
		report(len(out), total)
		return out, err
	}

	pages := make([][]albumResource, (total+limit-1)/limit)
	pages[0] = first.Data
	var (
		mu   sync.Mutex
		done = len(first.Data)
	)
	report(done, total)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, syncConcurrency)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for i := 1; i < len(pages); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			var p page[albumResource]
			if err := c.get(ctx, fmt.Sprintf("/me/library/albums?limit=%d&offset=%d", limit, i*limit), &p); err != nil {
				select {
				case errCh <- err:
				default:
				}
				cancel()
				return
			}
			mu.Lock()
			pages[i] = p.Data
			done += len(p.Data)
			n := done
			mu.Unlock()
			report(n, total)
		}(i)
	}
	wg.Wait()
	select {
	case err := <-errCh:
		return nil, err
	default:
	}

	// The total can drift while we page (albums added mid-sync); the
	// offset pages cover what existed, and a later sync picks up the rest.
	out := make([]Album, 0, total)
	seen := make(map[string]bool, total)
	for _, p := range pages {
		for _, r := range p {
			if !seen[r.ID] {
				seen[r.ID] = true
				out = append(out, toAlbum(r))
			}
		}
	}
	return out, nil
}

// AlbumTracks returns the library tracks of a library album, in Apple's order.
func (c *Client) AlbumTracks(ctx context.Context, albumID string) ([]Track, error) {
	var out []Track
	endpoint := "/me/library/albums/" + url.PathEscape(albumID) + "/tracks?limit=100"
	err := paginate(ctx, c, endpoint, func(r songResource) { out = append(out, toTrack(r)) })
	return out, err
}

// Storefront returns the account's storefront id, e.g. "us".
func (c *Client) Storefront(ctx context.Context) (string, error) {
	var p page[struct {
		ID string `json:"id"`
	}]
	if err := c.get(ctx, "/me/storefront", &p); err != nil {
		return "", err
	}
	if len(p.Data) == 0 {
		return "", errors.New("apple music: empty storefront response")
	}
	return p.Data[0].ID, nil
}
