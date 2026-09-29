package devtoken

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func jwtWith(kid string, exp time.Time, origin bool) string {
	enc := base64.RawURLEncoding.EncodeToString
	claims := fmt.Sprintf(`{"iss":"X","exp":%d}`, exp.Unix())
	if origin {
		claims = fmt.Sprintf(`{"iss":"X","exp":%d,"root_https_origin":["apple.com"]}`, exp.Unix())
	}
	return enc(fmt.Appendf(nil, `{"typ":"JWT","alg":"ES256","kid":%q}`, kid)) + "." + enc([]byte(claims)) + ".c2ln"
}

func jwt(exp time.Time) string { return jwtWith("K", exp, false) }

func TestChain(t *testing.T) {
	ctx := context.Background()
	valid := jwt(time.Now().Add(time.Hour))
	expired := jwt(time.Now().Add(-time.Hour))

	t.Setenv(EnvVar, "")
	c := Chain{Env{}, Static{"old", expired}, Static{"cfg", valid}}
	tok, src, err := c.Resolve(ctx)
	if err != nil || tok.Value != valid || src != "cfg" || tok.Origin != "" {
		t.Fatalf("got %+v %q %v", tok, src, err)
	}

	t.Setenv(EnvVar, "  "+valid+"\n")
	if _, src, _ := c.Resolve(ctx); src != "$"+EnvVar {
		t.Fatalf("env should win, got %q", src)
	}

	t.Setenv(EnvVar, "")
	_, _, err = Chain{Env{}, Static{"cfg", ""}}.Resolve(ctx)
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v", err)
	}
}

func TestExpiryAndOrigin(t *testing.T) {
	exp := time.Unix(1900000000, 0)
	if got, ok := Expiry(jwt(exp)); !ok || !got.Equal(exp) {
		t.Fatalf("got %v %v", got, ok)
	}
	if _, ok := Expiry("not-a-jwt"); ok {
		t.Fatal("expected !ok")
	}
	if o := OriginFor(jwtWith("K", exp, true)); o != "https://music.apple.com" {
		t.Fatalf("origin = %q", o)
	}
}

func TestWebPlayer(t *testing.T) {
	future := time.Now().Add(60 * 24 * time.Hour)
	want := jwtWith(webPlayerKid, future, true)
	other := jwtWith("97DQU9QUD6", future, true)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		switch r.URL.Path {
		case "/us/browse":
			fmt.Fprint(w, `<html><script type="module" crossorigin src="/assets/index~abc123.js"></script></html>`)
		case "/assets/index~abc123.js":
			fmt.Fprintf(w, `const a="%s",yo="2638.11.0-external",Ua="%s";`, other, want)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cache := filepath.Join(t.TempDir(), "webplayer-token.json")
	wp := WebPlayer{CacheFile: cache, BaseURL: srv.URL}
	tok, err := wp.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != want || tok.Origin != "https://music.apple.com" {
		t.Fatalf("got %+v", tok)
	}
	if _, err := wp.Token(context.Background()); err != nil || hits != 2 {
		t.Fatalf("second call should come from cache (hits=%d, err=%v)", hits, err)
	}

	// A cached token close to expiry is re-fetched.
	soon := jwtWith(webPlayerKid, time.Now().Add(24*time.Hour), true)
	wp.writeCache(soon)
	if tok, _ := wp.Token(context.Background()); tok.Value != want || hits != 4 {
		t.Fatalf("near-expiry cache not refreshed (hits=%d)", hits)
	}
}
