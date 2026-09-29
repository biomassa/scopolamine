//go:build linux

// Derived from vibez (https://github.com/simonepelosi/vibez),
// Copyright (c) 2025 Simone Pelosi, MIT License.

package cdp

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"text/template"
	"time"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/biomassa/scopolamine/internal/player"
)

//go:embed web/musickit.html
var musickitHTML string

// Player plays Apple Music through MusicKit JS in a Playwright-driven
// headless Chrome. Chrome's Widevine CDM decrypts and decodes the stream
// (256 kbps AAC, the web player's maximum) and outputs to PipeWire/Pulse.
type Player struct {
	// OnUserToken is called when MusicKit refreshes the user token.
	OnUserToken func(string)

	pw           *playwright.Playwright
	page         playwright.Page
	closeBrowser func()
	srv          *http.Server

	mu    sync.RWMutex
	state player.State
	bcast player.Broadcast

	readyCh   chan struct{}
	readyOnce sync.Once
	errCh     chan error
	closeOnce sync.Once
}

var _ player.Player = (*Player)(nil)

func renderHTML(devToken, userToken, version string) (string, error) {
	tmpl, err := template.New("musickit").Parse(musickitHTML)
	if err != nil {
		return "", err
	}
	// Tokens are interpolated into single-quoted JS strings. JWTs and Music
	// User Tokens are base64-ish, but refuse anything that could break out.
	for _, t := range []string{devToken, userToken} {
		if strings.ContainsAny(t, "'\\\n\r<>") {
			return "", fmt.Errorf("cdp: token contains unexpected characters")
		}
	}
	var b strings.Builder
	err = tmpl.Execute(&b, map[string]string{
		"DeveloperToken": devToken,
		"UserToken":      userToken,
		"Version":        version,
	})
	return b.String(), err
}

// Options configures New.
type Options struct {
	DevToken  string
	UserToken string
	// Origin is the web origin the developer token is restricted to
	// (e.g. https://music.apple.com for the web player's token). When set,
	// the MusicKit page is served at that origin through request
	// interception, so MusicKit's requests carry the Origin Apple expects.
	// Empty serves the page from a local 127.0.0.1 server.
	Origin  string
	Version string
}

// New starts the browser and loads the MusicKit page. EnsureBrowser must have
// been called first. Call WaitReady before issuing commands.
func New(o Options) (*Player, error) {
	html, err := renderHTML(o.DevToken, o.UserToken, o.Version)
	if err != nil {
		return nil, err
	}
	p := &Player{
		readyCh: make(chan struct{}),
		errCh:   make(chan error, 1),
		state:   player.State{QueueIndex: -1, Volume: 1},
	}
	pw, pg, closeBrowser, err := startBrowser(true)
	if err != nil {
		return nil, err
	}
	p.pw, p.page, p.closeBrowser = pw, pg, closeBrowser

	url, srv, err := servePage(pg, o.Origin, "/scopolamine/player", html)
	if err != nil {
		_ = p.Close()
		return nil, err
	}
	p.srv = srv

	pg.On("crash", func() { p.fail(fmt.Errorf("chrome page crashed")) })
	// Uncaught page errors are logged, not shown: the page reports real
	// failures through goError, and MusicKit throws harmless ones during
	// track transitions.
	pg.On("pageerror", func(err error) { p.notice(player.State{Log: "[page error] " + err.Error()}) })
	pg.On("console", func(msg playwright.ConsoleMessage) {
		if t := msg.Type(); t == "error" || t == "warning" {
			text := msg.Text()
			if strings.Contains(text, "Failed to load resource") {
				return // artwork/font CDN noise
			}
			p.notice(player.State{Log: "[chrome " + t + "] " + text})
		}
	})

	str := func(args []any) string {
		if len(args) == 0 {
			return ""
		}
		s, _ := args[0].(string)
		return s
	}
	bindings := map[string]playwright.ExposedFunction{
		"goReady": func(args ...any) any {
			p.mu.Lock()
			p.state.Ready = true
			p.state.Storefront = str(args)
			s := p.state
			p.mu.Unlock()
			p.bcast.Send(s)
			p.readyOnce.Do(func() { close(p.readyCh) })
			return nil
		},
		"goNeedsAuth": func(_ ...any) any {
			p.notice(player.State{NeedsAuth: true})
			select {
			case p.errCh <- fmt.Errorf("apple music session expired; run `scopolamine login`"):
			default:
			}
			return nil
		},
		"goState": func(args ...any) any {
			var js jsState
			if json.Unmarshal([]byte(str(args)), &js) == nil {
				p.apply(js)
			}
			return nil
		},
		"goUserToken": func(args ...any) any {
			if t := str(args); t != "" && p.OnUserToken != nil {
				p.OnUserToken(t)
			}
			return nil
		},
		"goError":   func(args ...any) any { p.fail(fmt.Errorf("musickit: %s", str(args))); return nil },
		"goSkipped": func(args ...any) any { p.notice(player.State{SkippedID: str(args)}); return nil },
		"goLog":     func(args ...any) any { p.notice(player.State{Log: str(args)}); return nil },
	}
	for name, fn := range bindings {
		if err := pg.ExposeFunction(name, fn); err != nil {
			_ = p.Close()
			return nil, fmt.Errorf("cdp: expose %s: %w", name, err)
		}
	}

	go func() {
		if _, err := pg.Goto(url); err != nil {
			p.fail(fmt.Errorf("cdp navigate: %w", err))
		}
	}()
	return p, nil
}

type jsState struct {
	IsPlaying     bool    `json:"isPlaying"`
	PlaybackState int     `json:"playbackState"`
	CurrentTime   float64 `json:"currentTime"`
	Volume        float64 `json:"volume"`
	Bitrate       int     `json:"bitrate"`
	QueueIndex    int     `json:"queueIndex"`
	QueueLength   int     `json:"queueLength"`
	NowPlaying    *struct {
		ID         string `json:"id"`
		CatalogID  string `json:"catalogId"`
		Title      string `json:"title"`
		Artist     string `json:"artist"`
		Album      string `json:"album"`
		ArtworkURL string `json:"artworkURL"`
		DurationMs int64  `json:"durationMs"`
	} `json:"nowPlaying"`
}

func (p *Player) apply(js jsState) {
	p.mu.Lock()
	s := p.state
	s.Playing = js.IsPlaying
	// MusicKit PlaybackStates: 1 loading, 7 waiting, 8 stalled.
	s.Loading = js.PlaybackState == 1 || js.PlaybackState == 7 || js.PlaybackState == 8
	s.Position = time.Duration(js.CurrentTime * float64(time.Second))
	s.Volume = js.Volume
	s.BitrateKbps = js.Bitrate
	s.QueueIndex = js.QueueIndex
	s.QueueLength = js.QueueLength
	s.Track = nil
	if n := js.NowPlaying; n != nil {
		s.Track = &player.NowPlaying{
			ID: n.ID, CatalogID: n.CatalogID, Title: n.Title, Artist: n.Artist,
			Album: n.Album, ArtworkURL: n.ArtworkURL,
			Duration: time.Duration(n.DurationMs) * time.Millisecond,
		}
	}
	p.state = s
	p.mu.Unlock()
	p.bcast.Send(s)
}

// notice sends the current state with the transient fields of n set.
func (p *Player) notice(n player.State) {
	p.mu.RLock()
	s := p.state
	p.mu.RUnlock()
	s.Error, s.Log, s.SkippedID, s.NeedsAuth = n.Error, n.Log, n.SkippedID, n.NeedsAuth
	p.bcast.Send(s)
}

func (p *Player) fail(err error) {
	select {
	case p.errCh <- err:
	default:
	}
	p.notice(player.State{Error: err.Error()})
}

// WaitReady blocks until MusicKit is configured with the user token.
func (p *Player) WaitReady(ctx context.Context) error {
	select {
	case <-p.readyCh:
		return nil
	case err := <-p.errCh:
		return err
	case <-ctx.Done():
		return fmt.Errorf("apple music player did not start: %w", ctx.Err())
	}
}

func (p *Player) eval(js string) error {
	go func() { _, _ = p.page.Evaluate(js) }()
	return nil
}

// PlayTracks queues library song ids and starts at ids[start].
func (p *Player) PlayTracks(ids []string, start int) error {
	if len(ids) == 0 {
		return nil
	}
	if start < 0 || start >= len(ids) {
		start = 0
	}
	arg, err := json.Marshal(map[string]any{"ids": ids, "start": start})
	if err != nil {
		return err
	}
	lit, err := json.Marshal(string(arg))
	if err != nil {
		return err
	}
	return p.eval(fmt.Sprintf(`window.scPlayTracks && window.scPlayTracks(%s)`, lit))
}

func (p *Player) Play() error     { return p.eval(`window.scPlay && window.scPlay()`) }
func (p *Player) Pause() error    { return p.eval(`window.scPause && window.scPause()`) }
func (p *Player) Toggle() error   { return p.eval(`window.scToggle && window.scToggle()`) }
func (p *Player) Stop() error     { return p.eval(`window.scStop && window.scStop()`) }
func (p *Player) Next() error     { return p.eval(`window.scNext && window.scNext()`) }
func (p *Player) Previous() error { return p.eval(`window.scPrev && window.scPrev()`) }

func (p *Player) Seek(pos time.Duration) error {
	return p.eval(fmt.Sprintf(`window.scSeek && window.scSeek(%f)`, pos.Seconds()))
}

func (p *Player) SetVolume(v float64) error {
	v = min(1, max(0, v))
	return p.eval(fmt.Sprintf(`window.scSetVolume && window.scSetVolume(%f)`, v))
}

func (p *Player) State() player.State {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

func (p *Player) Subscribe() <-chan player.State { return p.bcast.Subscribe() }

// Close shuts the browser, driver and page server down.
func (p *Player) Close() error {
	p.closeOnce.Do(func() {
		if p.closeBrowser != nil {
			p.closeBrowser()
		}
		if p.pw != nil {
			_ = p.pw.Stop()
		}
		if p.srv != nil {
			_ = p.srv.Close()
		}
		p.bcast.Close()
	})
	return nil
}
