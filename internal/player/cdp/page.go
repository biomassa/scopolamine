//go:build linux

package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
)

// startBrowser runs the Playwright driver and launches Chrome with one page.
func startBrowser(headless bool) (*playwright.Playwright, playwright.Page, func(), error) {
	pw, err := runPlaywright()
	if err != nil {
		return nil, nil, nil, err
	}
	chromePath := HelperPath()
	if _, err := os.Stat(chromePath); err != nil {
		chromePath = ChromePath()
	}
	pg, closeBrowser, err := launchBrowser(pw, chromePath, headless, false)
	if err != nil {
		_ = pw.Stop()
		return nil, nil, nil, fmt.Errorf("cdp: launch browser: %w", err)
	}
	return pw, pg, closeBrowser, nil
}

// servePage makes html reachable to pg and returns its URL. With an origin
// the page is answered by request interception at origin+path, giving it
// that origin; otherwise a local server is started (returned so the caller
// can close it).
func servePage(pg playwright.Page, origin, path, html string) (string, *http.Server, error) {
	if origin != "" {
		url := strings.TrimRight(origin, "/") + path
		err := pg.Route(url, func(r playwright.Route) {
			_ = r.Fulfill(playwright.RouteFulfillOptions{
				Status:      playwright.Int(200),
				ContentType: playwright.String("text/html; charset=utf-8"),
				Body:        html,
			})
		})
		if err != nil {
			return "", nil, fmt.Errorf("cdp: route %s: %w", url, err)
		}
		return url, nil, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("cdp: listen: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return fmt.Sprintf("http://127.0.0.1:%d%s", ln.Addr().(*net.TCPAddr).Port, path), srv, nil
}

// userTokenCookie is where the music.apple.com web app keeps the
// Music-User-Token after sign-in.
const userTokenCookie = "media-user-token"

const doneHTML = `<!DOCTYPE html><html><head><meta charset="utf-8"><title>scopolamine — connected</title>
<style>body{background:#16161a;color:#98c379;font:16px system-ui,sans-serif;display:flex;
align-items:center;justify-content:center;min-height:100vh;margin:0}</style></head>
<body><p>Connected to Apple Music. This window will close; return to the terminal.</p></body></html>`

// LoginWindow signs in through a visible Chrome window, for origin-restricted
// developer tokens (which MusicKit rejects on a localhost page in the user's
// own browser). EnsureBrowser must have been called first.
//
// If loginHTML (the page from package auth) is non-empty it is served at
// origin and its POST to ./callback delivers the token. With an empty
// loginHTML the window opens the real site at origin (music.apple.com) and
// the user signs in with its own Sign In button. Either way the web app's
// media-user-token cookie is also watched, and whichever arrives first wins.
//
// The empty-loginHTML mode exists because MusicKit's authorize() started
// with the web player's token from our page ends on an Apple "upsell"
// screen instead of a sign-in form.
func LoginWindow(ctx context.Context, origin, loginHTML string) (string, error) {
	if origin == "" {
		return "", errors.New("cdp: LoginWindow needs an origin")
	}
	origin = strings.TrimRight(origin, "/")
	start := origin + "/scopolamine/login"
	if loginHTML == "" {
		start = origin + "/"
	}
	pw, pg, closeBrowser, err := startBrowser(false)
	if err != nil {
		return "", err
	}
	defer func() {
		closeBrowser()
		_ = pw.Stop()
	}()
	bctx := pg.Context()

	// Routes are registered on the browser context so they also apply to
	// any popup or navigation the sign-in flow opens.
	fulfillHTML := func(html string) func(playwright.Route) {
		return func(r playwright.Route) {
			_ = r.Fulfill(playwright.RouteFulfillOptions{
				Status:      playwright.Int(200),
				ContentType: playwright.String("text/html; charset=utf-8"),
				Body:        html,
			})
		}
	}
	if loginHTML != "" {
		if err := bctx.Route(origin+"/scopolamine/login", fulfillHTML(loginHTML)); err != nil {
			return "", fmt.Errorf("cdp: route login page: %w", err)
		}
	}
	if err := bctx.Route(origin+"/scopolamine/done", fulfillHTML(doneHTML)); err != nil {
		return "", fmt.Errorf("cdp: route done page: %w", err)
	}
	tokenCh := make(chan string, 1)
	offer := func(t string) {
		if t != "" {
			select {
			case tokenCh <- t:
			default:
			}
		}
	}
	err = bctx.Route(origin+"/scopolamine/callback", func(r playwright.Route) {
		body, _ := r.Request().PostData()
		offer(parseCallback(body))
		_ = r.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(200), Body: "ok"})
	})
	if err != nil {
		return "", fmt.Errorf("cdp: route callback: %w", err)
	}
	if _, err := pg.Goto(start); err != nil {
		return "", fmt.Errorf("cdp: open login page: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case t := <-tokenCh:
			showDone(bctx, origin)
			return t, nil
		case <-ctx.Done():
			return "", errors.New("sign-in timed out")
		case <-tick.C:
			if len(bctx.Pages()) == 0 {
				return "", errors.New("login window closed before sign-in finished")
			}
			if cookies, err := bctx.Cookies(origin); err == nil {
				for _, c := range cookies {
					if c.Name == userTokenCookie {
						offer(c.Value)
					}
				}
			}
		}
	}
}

// showDone points the window at the "connected" page for a moment before it
// is closed.
func showDone(bctx playwright.BrowserContext, origin string) {
	pages := bctx.Pages()
	if len(pages) == 0 {
		return
	}
	pg := pages[len(pages)-1]
	if _, err := pg.Goto(origin + "/scopolamine/done"); err == nil {
		time.Sleep(1500 * time.Millisecond)
	}
}

func parseCallback(body string) string {
	var v struct {
		UserToken string `json:"user_token"`
	}
	if json.Unmarshal([]byte(body), &v) != nil {
		return ""
	}
	return v.UserToken
}
