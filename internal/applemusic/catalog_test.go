package applemusic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func catalogServer(t *testing.T) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	var added atomic.Bool
	album := func(id, name string, lib bool) map[string]any {
		a := map[string]any{"id": id, "type": "albums", "attributes": map[string]any{
			"name": name, "artistName": "Loscil", "releaseDate": "2006-05-22", "trackCount": 9, "isSingle": false,
			"artwork": map[string]any{"url": "https://img/{w}x{h}.jpg"}}}
		if lib {
			a["relationships"] = map[string]any{"library": map[string]any{"data": []any{map[string]any{"id": "l." + id}}}}
		} else {
			a["relationships"] = map[string]any{"library": map[string]any{"data": []any{}}}
		}
		return a
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "https://music.apple.com" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		enc := json.NewEncoder(w)
		switch p := strings.TrimPrefix(r.URL.Path, "/v1"); {
		case p == "/me/storefront":
			_ = enc.Encode(map[string]any{"data": []any{map[string]any{"id": "il"}}})
		case p == "/catalog/il/search":
			if q.Get("relate[albums]") != "library" || q.Get("types") != "artists,albums" {
				t.Errorf("search query %v", q)
			}
			_ = enc.Encode(map[string]any{"results": map[string]any{
				"artists": map[string]any{"data": []any{map[string]any{"id": "40886745", "attributes": map[string]any{"name": "Loscil"}}}},
				"albums":  map[string]any{"data": []any{album("1", "Plume", true), album("2", "Submers", false)}},
			}})
		case p == "/catalog/il/artists/40886745/albums":
			_ = enc.Encode(map[string]any{"data": []any{album("1", "Plume", false), album("2", "Submers", false)}})
		case p == "/catalog/il/albums":
			ids := strings.Split(q.Get("ids"), ",")
			var data []any
			for _, id := range ids {
				data = append(data, album(id, "x", id == "1" || (id == "2" && added.Load())))
			}
			_ = enc.Encode(map[string]any{"data": data})
		case p == "/me/library" && r.Method == http.MethodPost:
			if q.Get("ids[albums]") != "2" {
				t.Errorf("add query %v", q)
			}
			added.Store(true)
			w.WriteHeader(http.StatusAccepted)
		case p == "/me/library/albums/l.2":
			_ = enc.Encode(map[string]any{"data": []any{map[string]any{"id": "l.2", "attributes": map[string]any{
				"name": "Submers", "artistName": "Loscil", "releaseDate": "2002-01-01", "trackCount": 8}}}})
		case p == "/catalog/il/albums/2/tracks":
			_ = enc.Encode(map[string]any{"data": []any{
				map[string]any{"id": "900", "attributes": map[string]any{"name": "Argo", "trackNumber": 1, "discNumber": 1,
					"durationInMillis": 1000, "playParams": map[string]any{"id": "900", "kind": "song"}}},
				map[string]any{"id": "901", "attributes": map[string]any{"name": "Gone", "trackNumber": 2, "discNumber": 1}},
			}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	return srv, &added
}

func TestCatalog(t *testing.T) {
	srv, _ := catalogServer(t)
	defer srv.Close()
	c := New("dev", "user")
	c.Origin = "https://music.apple.com"
	c.SetBaseURL(srv.URL + "/v1")
	ctx := context.Background()

	res, err := c.Search(ctx, "loscil")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Artists) != 1 || res.Artists[0].Name != "Loscil" || len(res.Albums) != 2 {
		t.Fatalf("search: %+v", res)
	}
	if res.Albums[0].LibraryID != "l.1" || res.Albums[1].LibraryID != "" || res.Albums[0].ArtworkURL != "https://img/600x600.jpg" {
		t.Fatalf("library marks: %+v", res.Albums)
	}

	disc, err := c.ArtistAlbums(ctx, "40886745")
	if err != nil || len(disc) != 2 || disc[0].LibraryID != "l.1" || disc[1].LibraryID != "" {
		t.Fatalf("discography: %+v %v", disc, err)
	}

	tr, err := c.CatalogAlbumTracks(ctx, "2")
	if err != nil || len(tr) != 2 || tr[0].ID != "900" || !tr[0].Playable || tr[1].Playable {
		t.Fatalf("tracks: %+v %v", tr, err)
	}

	a, err := c.AddAlbumAndWait(ctx, "2")
	if err != nil || a.ID != "l.2" || a.CatalogID != "2" || a.Title != "Submers" {
		t.Fatalf("add: %+v %v", a, err)
	}
}

func TestDeleteLibraryAlbum(t *testing.T) {
	var deleted atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/me/library/albums/l.x":
			deleted.Store(true)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/me/library/albums/l.x":
			if deleted.Load() {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"l.x"}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	}))
	defer srv.Close()
	c := New("dev", "user")
	c.SetBaseURL(srv.URL + "/v1")
	if err := c.DeleteLibraryAlbum(context.Background(), "l.x"); err != nil || !deleted.Load() {
		t.Fatalf("delete: %v (deleted=%v)", err, deleted.Load())
	}
	if err := c.DeleteLibraryAlbum(context.Background(), "12345"); err == nil {
		t.Fatal("accepted a catalog id")
	}
}
