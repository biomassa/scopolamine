package devtoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// WebPlayer reads the developer token the music.apple.com web player ships
// in its JavaScript bundle. The token is restricted to apple.com origins, so
// everything using it must send Origin: https://music.apple.com (see
// Token.Origin). Apple rotates it; the result is cached in CacheFile and
// re-fetched when it is within refreshBefore of expiry.
//
// This is Apple's own web-player credential, not one issued to scopolamine,
// and may stop working whenever Apple changes the site. Fine for personal use.
type WebPlayer struct {
	CacheFile string // "" disables caching
	BaseURL   string // "" = https://music.apple.com; tests only
	Client    *http.Client
}

const (
	webPlayerKid  = "WebPlayKid"
	refreshBefore = 7 * 24 * time.Hour
	userAgent     = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"
)

var (
	reBundle = regexp.MustCompile(`src="(/assets/index~[^"]+\.js)"`)
	reJWT    = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
)

func (w WebPlayer) Name() string { return "music.apple.com web player" }

type cached struct {
	Token     string    `json:"token"`
	FetchedAt time.Time `json:"fetched_at"`
}

func (w WebPlayer) Token(ctx context.Context) (Token, error) {
	if t, ok := w.readCache(); ok {
		return Token{Value: t, Origin: OriginFor(t)}, nil
	}
	t, err := w.fetch(ctx)
	if err != nil {
		return Token{}, err
	}
	w.writeCache(t)
	return Token{Value: t, Origin: OriginFor(t)}, nil
}

// Refresh drops the cached token, e.g. after Apple rejected it.
func (w WebPlayer) Refresh() { _ = os.Remove(w.CacheFile) }

func (w WebPlayer) readCache() (string, bool) {
	if w.CacheFile == "" {
		return "", false
	}
	b, err := os.ReadFile(w.CacheFile)
	if err != nil {
		return "", false
	}
	var c cached
	if json.Unmarshal(b, &c) != nil || c.Token == "" {
		return "", false
	}
	if exp, ok := Expiry(c.Token); !ok || time.Until(exp) < refreshBefore {
		return "", false
	}
	return c.Token, true
}

func (w WebPlayer) writeCache(t string) {
	if w.CacheFile == "" {
		return
	}
	b, _ := json.Marshal(cached{Token: t, FetchedAt: time.Now()})
	if os.MkdirAll(filepath.Dir(w.CacheFile), 0o700) == nil {
		_ = os.WriteFile(w.CacheFile, b, 0o600)
	}
}

func (w WebPlayer) get(ctx context.Context, url string) ([]byte, error) {
	c := w.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

func (w WebPlayer) fetch(ctx context.Context) (string, error) {
	base := w.BaseURL
	if base == "" {
		base = "https://music.apple.com"
	}
	page, err := w.get(ctx, base+"/us/browse")
	if err != nil {
		return "", err
	}
	m := reBundle.FindSubmatch(page)
	if m == nil {
		return "", errors.New("web player bundle not found (site layout changed?)")
	}
	js, err := w.get(ctx, base+string(m[1]))
	if err != nil {
		return "", err
	}
	if t := pickToken(reJWT.FindAll(js, -1)); t != "" {
		return t, nil
	}
	return "", errors.New("no developer token in web player bundle (site layout changed?)")
}

// pickToken prefers the web player's own key; the bundle carries others.
func pickToken(found [][]byte) string {
	fallback := ""
	for _, b := range found {
		t := string(b)
		h, c := parseHeader(t), parseClaims(t)
		if h == nil || c == nil || h.Alg != "ES256" {
			continue
		}
		if h.Kid == webPlayerKid {
			return t
		}
		if fallback == "" && len(c.RootHTTPSOrigin) > 0 {
			fallback = t
		}
	}
	return fallback
}
