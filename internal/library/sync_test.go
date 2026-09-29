package library_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/biomassa/scopolamine/internal/applemusic"
	"github.com/biomassa/scopolamine/internal/library"
)

type fakeAlbum struct {
	id, title, artist, date string
	tracks                  int
}

// fakeAPI serves /v1/me/library/albums (paged with "next", 100 per page like
// Apple) and /v1/me/library/albums/{id}/tracks.
//
// withTotal adds meta.total (which makes the client fetch pages in parallel)
// and rate-limits the offset=200 page once, to exercise the retry.
func fakeAPI(t *testing.T, albums *[]fakeAlbum, trackHits *int32, withTotal bool) *httptest.Server {
	t.Helper()
	var limited atomic.Bool
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dev" || r.Header.Get("Music-User-Token") != "user" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/v1")
		switch {
		case path == "/me/library/albums":
			off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			if withTotal && off == 200 && limited.CompareAndSwap(false, true) {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			const lim = 100
			var data []map[string]any
			all := *albums
			for i := off; i < len(all) && i < off+lim; i++ {
				a := all[i]
				data = append(data, map[string]any{
					"id": a.id, "type": "library-albums",
					"attributes": map[string]any{
						"name": a.title, "artistName": a.artist, "releaseDate": a.date,
						"trackCount": a.tracks, "genreNames": []string{"Electronic"},
						"artwork":    map[string]any{"url": "https://img/{w}x{h}bb.jpg"},
						"playParams": map[string]any{"id": a.id, "kind": "album", "isLibrary": true},
					},
				})
			}
			resp := map[string]any{"data": data}
			if withTotal {
				resp["meta"] = map[string]any{"total": len(all)}
			}
			if off+lim < len(all) {
				resp["next"] = fmt.Sprintf("/v1/me/library/albums?offset=%d", off+lim)
			}
			_ = json.NewEncoder(w).Encode(resp)
		case strings.HasPrefix(path, "/me/library/albums/") && strings.HasSuffix(path, "/tracks"):
			atomic.AddInt32(trackHits, 1)
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/me/library/albums/"), "/tracks")
			// Out of order on purpose: the store must sort disc/track.
			gone := song(id, 3, 1, "Gone")
			delete(gone["attributes"].(map[string]any), "playParams") // not in this storefront
			data := []map[string]any{
				song(id, 2, 1, "Second"), song(id, 1, 2, "Disc1 Two"), song(id, 1, 1, "Disc1 One"), gone,
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		default:
			http.NotFound(w, r)
		}
	}))
}

func song(album string, disc, n int, name string) map[string]any {
	return map[string]any{
		"id": fmt.Sprintf("i.%s-%d-%d", album, disc, n), "type": "library-songs",
		"attributes": map[string]any{
			"name": name, "artistName": "Artist", "albumName": album,
			"discNumber": disc, "trackNumber": n, "durationInMillis": 61000,
			"playParams": map[string]any{"id": "x", "kind": "song", "isLibrary": true, "catalogId": "123"},
		},
	}
}

func TestSyncAndBrowse(t *testing.T) {
	t.Run("serial next links", func(t *testing.T) { testSyncAndBrowse(t, false) })
	t.Run("parallel with total and 429", func(t *testing.T) { testSyncAndBrowse(t, true) })
}

func testSyncAndBrowse(t *testing.T, withTotal bool) {
	ctx := context.Background()
	var albums []fakeAlbum
	for i := range 250 { // three pages
		albums = append(albums, fakeAlbum{fmt.Sprintf("l.%03d", i), fmt.Sprintf("Album %03d", i), fmt.Sprintf("Artist %d", i%10), fmt.Sprintf("%d-01-01", 1990+i%20), 3})
	}
	albums = append(albums,
		fakeAlbum{"l.boc1", "Geogaddi", "Boards of Canada", "2002-02-18", 23},
		fakeAlbum{"l.boc0", "Music Has the Right to Children", "Boards of Canada", "1998-04-20", 18},
		fakeAlbum{"l.the", "Tender Buttons", "The Broadcast", "2005-09-19", 14},
		fakeAlbum{"l.va", "Warp10+1", "Various Artists", "1999", 10},
		fakeAlbum{"l.nodate", "Undated", "Boards of Canada", "", 1},
	)
	var hits int32
	srv := fakeAPI(t, &albums, &hits, withTotal)
	defer srv.Close()

	c := applemusic.New("dev", "user")
	c.SetBaseURL(srv.URL + "/v1")
	store, err := library.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	n, err := library.SyncAppleAlbums(ctx, store, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(albums) {
		t.Fatalf("synced %d albums, want %d", n, len(albums))
	}

	artists, err := store.Artists(ctx, library.SourceApple)
	if err != nil {
		t.Fatal(err)
	}
	if got := artists[len(artists)-1].Name; got != library.VariousArtists {
		t.Errorf("last artist = %q, want compilations last", got)
	}
	var sawBroadcast bool
	for i, a := range artists {
		if a.Name == "The Broadcast" {
			sawBroadcast = true
			// A leading "The" is ignored, so "broadcast" follows "boards of canada".
			if i == 0 || artists[i-1].Name != "Boards of Canada" {
				t.Errorf("The Broadcast sorted after %q, want after Boards of Canada", artists[i-1].Name)
			}
		}
	}
	if !sawBroadcast {
		t.Error("The Broadcast missing")
	}

	boc, err := store.AlbumsByArtist(ctx, library.SourceApple, "boards of canada")
	if err != nil {
		t.Fatal(err)
	}
	if len(boc) != 3 || boc[0].Title != "Music Has the Right to Children" || boc[1].Title != "Geogaddi" || boc[2].Title != "Undated" {
		t.Fatalf("albums by year wrong: %+v", boc)
	}
	if boc[0].Year != 1998 || boc[0].ArtworkURL != "https://img/600x600bb.jpg" || boc[0].Genre != "Electronic" {
		t.Errorf("album fields: %+v", boc[0])
	}

	// Tracks: fetched once, then served from the cache, sorted by disc/track.
	for range 2 {
		tr, err := library.AppleTracks(ctx, store, c, "l.boc1")
		if err != nil {
			t.Fatal(err)
		}
		if len(tr) != 4 || tr[0].Title != "Disc1 One" || tr[1].Title != "Disc1 Two" || tr[2].Title != "Second" {
			t.Fatalf("track order: %+v", tr)
		}
		if !tr[0].Playable || tr[3].Playable {
			t.Fatalf("playable flags: %v %v", tr[0].Playable, tr[3].Playable)
		}
		if tr[0].Duration.Seconds() != 61 || tr[0].CatalogID != "123" {
			t.Errorf("track fields: %+v", tr[0])
		}
	}
	if hits != 1 {
		t.Errorf("tracks endpoint hit %d times, want 1 (cache)", hits)
	}

	// Re-sync after an album was removed from the library: it disappears
	// along with its cached tracks; the others keep their track cache.
	if _, err := library.AppleTracks(ctx, store, c, "l.boc0"); err != nil {
		t.Fatal(err)
	}
	albums = albums[:len(albums)-4] // drop boc0, the, va, nodate
	albums = append(albums, fakeAlbum{"l.new", "New One", "Boards of Canada", "2013", 1})
	if _, err := library.SyncAppleAlbums(ctx, store, c, nil); err != nil {
		t.Fatal(err)
	}
	boc, _ = store.AlbumsByArtist(ctx, library.SourceApple, "Boards of Canada")
	if len(boc) != 2 || boc[0].ID != "l.boc1" || boc[1].ID != "l.new" {
		t.Fatalf("after resync: %+v", boc)
	}
	if _, synced, _ := store.Tracks(ctx, "l.boc1"); !synced {
		t.Error("track cache of unchanged album was dropped")
	}
	if _, _, err := store.Tracks(ctx, "l.boc0"); err == nil {
		t.Error("removed album still present")
	}
}

func TestUnauthorized(t *testing.T) {
	var albums []fakeAlbum
	var hits int32
	srv := fakeAPI(t, &albums, &hits, false)
	defer srv.Close()
	c := applemusic.New("dev", "wrong")
	c.SetBaseURL(srv.URL + "/v1")
	_, err := c.LibraryAlbums(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("err = %v, want unauthorized", err)
	}
}

func TestMigrateDropsOldTrackCache(t *testing.T) {
	path := t.TempDir() + "/old.db"
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE albums (id TEXT PRIMARY KEY, source TEXT NOT NULL, catalog_id TEXT NOT NULL DEFAULT '', title TEXT NOT NULL,
			artist TEXT NOT NULL, artist_key TEXT NOT NULL, year INTEGER NOT NULL DEFAULT 0, release_date TEXT NOT NULL DEFAULT '',
			date_added TEXT NOT NULL DEFAULT '', track_count INTEGER NOT NULL DEFAULT 0, genre TEXT NOT NULL DEFAULT '',
			artwork_url TEXT NOT NULL DEFAULT '', tracks_synced_at INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE tracks (id TEXT NOT NULL, album_id TEXT NOT NULL, catalog_id TEXT NOT NULL DEFAULT '', title TEXT NOT NULL,
			artist TEXT NOT NULL, disc INTEGER NOT NULL DEFAULT 0, number INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (album_id, id))`,
		`INSERT INTO albums (id, source, title, artist, artist_key, tracks_synced_at) VALUES ('l.x', 'apple', 'X', 'A', 'a', 123)`,
		`INSERT INTO tracks (id, album_id, title, artist) VALUES ('i.1', 'l.x', 'T', 'A')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	s, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tr, synced, err := s.Tracks(context.Background(), "l.x")
	if err != nil || synced || len(tr) != 0 {
		t.Fatalf("old track cache kept: %v %v %v", tr, synced, err)
	}
	if a, err := s.Album(context.Background(), "l.x"); err != nil || a.Title != "X" {
		t.Fatalf("album lost in migration: %+v %v", a, err)
	}
}
