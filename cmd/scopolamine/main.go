// Command scopolamine is an album-focused Apple Music TUI for Linux.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/applemusic"
	"github.com/biomassa/scopolamine/internal/auth"
	"github.com/biomassa/scopolamine/internal/config"
	"github.com/biomassa/scopolamine/internal/cover"
	"github.com/biomassa/scopolamine/internal/devtoken"
	"github.com/biomassa/scopolamine/internal/library"
	"github.com/biomassa/scopolamine/internal/localscan"
	"github.com/biomassa/scopolamine/internal/mpris"
	"github.com/biomassa/scopolamine/internal/player"
	"github.com/biomassa/scopolamine/internal/player/cdp"
	"github.com/biomassa/scopolamine/internal/player/mpv"
	"github.com/biomassa/scopolamine/internal/player/router"
	"github.com/biomassa/scopolamine/internal/tui"
)

const version = "0.5.4"

const usage = `scopolamine — Apple Music library browser for the terminal

Usage:
  scopolamine [--offline] [--theme NAME]   start the TUI
  scopolamine login         sign in to Apple Music in your browser
  scopolamine logout        forget the Apple Music user token
  scopolamine sync          refresh the album list from Apple Music
  scopolamine token [refresh]  show (or re-fetch) the developer token
  scopolamine version       (or --version)

Developer token (required): set $` + devtoken.EnvVar + ` or "developer_token" in
` + "%s" + `
`

func main() {
	flag.Usage = func() { fmt.Fprintf(os.Stderr, usage, config.DefaultPath()) }
	offline := flag.Bool("offline", false, "browse the cached library without starting the player or syncing")
	showVersion := flag.Bool("version", false, "print the version and exit")
	themeFlag := flag.String("theme", "", "use a color theme for this run only (see the picker, T)")
	flag.Parse()
	if *showVersion {
		fmt.Println("scopolamine", version)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *themeFlag != "" && !tui.ValidTheme(*themeFlag) {
		fmt.Fprintf(os.Stderr, "scopolamine: unknown theme %q; themes: %s\n", *themeFlag, strings.Join(tui.ThemeNames(), ", "))
		os.Exit(2)
	}

	var err error
	switch cmd := flag.Arg(0); cmd {
	case "":
		err = runTUI(ctx, *offline, *themeFlag)
	case "login":
		err = cmdLogin(ctx)
	case "logout":
		err = cmdLogout()
	case "sync":
		err = cmdSync(ctx)
	case "token":
		if flag.Arg(1) == "refresh" {
			webPlayerSource().Refresh()
		}
		err = cmdToken(ctx)
	case "version":
		fmt.Println("scopolamine", version)
	case "help":
		flag.Usage()
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "scopolamine:", err)
		os.Exit(1)
	}
}

func webPlayerSource() devtoken.WebPlayer {
	return devtoken.WebPlayer{CacheFile: filepath.Join(config.CacheDir(), "webplayer-token.json")}
}

func tokenChain(cfg *config.Config) devtoken.Chain {
	return devtoken.Chain{
		devtoken.Env{},
		devtoken.Static{Label: "config developer_token", Value: cfg.DeveloperToken},
		webPlayerSource(),
	}
}

func devToken(ctx context.Context, cfg *config.Config) (devtoken.Token, error) {
	t, _, err := tokenChain(cfg).Resolve(ctx)
	if err != nil {
		return devtoken.Token{}, fmt.Errorf(`%w

scopolamine needs an Apple Music developer token (a JWT that identifies the
app to Apple). It normally uses the token of music.apple.com; you
can also provide one with:
  export %s=<token>
  or "developer_token": "<token>" in %s`, err, devtoken.EnvVar, config.DefaultPath())
	}
	return t, nil
}

func newClient(dt devtoken.Token, cfg *config.Config) *applemusic.Client {
	c := applemusic.New(dt.Value, cfg.UserToken)
	c.Origin = dt.Origin
	return c
}

// unauthorizedHint explains a 401/403 from Apple.
func unauthorizedHint(err error) error {
	if errors.Is(err, applemusic.ErrUnauthorized) {
		return fmt.Errorf("%w\nTry `scopolamine token refresh`, then `scopolamine login`", err)
	}
	return err
}

func cmdToken(ctx context.Context) error {
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	t, src, err := tokenChain(cfg).Resolve(ctx)
	if err != nil {
		return err
	}
	fmt.Println("developer token from:", src)
	if t.Origin != "" {
		fmt.Println("restricted to origin:", t.Origin)
	}
	if exp, ok := devtoken.Expiry(t.Value); ok {
		fmt.Printf("expires: %s (in %s)\n", exp.Format(time.RFC3339), time.Until(exp).Round(time.Hour))
	}
	fmt.Println("user token:", map[bool]string{true: "present", false: "missing — run `scopolamine login`"}[cfg.UserToken != ""])
	return nil
}

func cmdLogin(ctx context.Context) error {
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	return login(ctx, cfg)
}

func login(ctx context.Context, cfg *config.Config) error {
	dt, err := devToken(ctx, cfg)
	if err != nil {
		return err
	}
	var ut string
	if dt.Origin == "" {
		ut, err = auth.Login(ctx, dt.Value, cfg.AuthPort, func(s string) { fmt.Println(s) })
	} else {
		// Origin-restricted token: MusicKit refuses it on a localhost page, so
		// sign in through a Chrome window whose page lives at that origin.
		ut, err = loginWindow(ctx, dt)
	}
	if err != nil {
		return err
	}
	cfg.UserToken = ut
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Println("✓ Signed in to Apple Music.")
	return nil
}

func cmdLogout() error {
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	cfg.UserToken = ""
	return cfg.Save()
}

func openLibrary() (*library.Store, error) {
	return library.Open(library.DefaultPath(config.CacheDir()))
}

func cmdSync(ctx context.Context) error {
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	dt, err := devToken(ctx, cfg)
	if err != nil {
		return err
	}
	if cfg.UserToken == "" {
		return errors.New("not signed in; run `scopolamine login`")
	}
	store, err := openLibrary()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	var progMu sync.Mutex
	n, err := library.SyncAppleAlbums(ctx, store, newClient(dt, cfg), func(n, total int) {
		progMu.Lock()
		defer progMu.Unlock()
		if total > 0 {
			fmt.Printf("\r%d/%d albums…", n, total)
		} else {
			fmt.Printf("\r%d albums…", n)
		}
	})
	fmt.Println()
	if err != nil {
		return unauthorizedHint(err)
	}
	fmt.Printf("✓ %d albums synced\n", n)
	return nil
}

func runTUI(ctx context.Context, offline bool, themeOverride string) error {
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	store, err := openLibrary()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	theme := cfg.Theme
	if themeOverride != "" {
		theme = themeOverride
	}
	tui.ApplyTheme(theme)

	sessionPath := filepath.Join(config.CacheDir(), "session.json")
	deps := tui.Deps{Store: store, Volume: cfg.Volume, Resume: tui.LoadSession(sessionPath), Version: version}
	var cfgMu sync.Mutex
	deps.SaveTheme = func(name string) error {
		cfgMu.Lock()
		defer cfgMu.Unlock()
		cfg.Theme = name
		return cfg.Save()
	}
	var dt devtoken.Token
	if !offline {
		if dt, err = devToken(ctx, cfg); err != nil {
			return err
		}
		if cfg.UserToken == "" {
			if err := login(ctx, cfg); err != nil {
				return err
			}
		}
		client := newClient(dt, cfg)
		deps.Src, deps.Catalog = client, client
		deps.AutoSync = time.Since(store.LastSync(ctx, library.SourceApple)) > 12*time.Hour
	}

	if cover.Supported() {
		cw, ch := cover.CellSize()
		deps.Covers = cover.New(filepath.Join(config.CacheDir(), "art"), cw, ch)
	}

	// One player for the TUI and MPRIS: the router forwards to the Apple
	// Music engine or the mpv engine.
	rt := router.New(cfg.Volume)
	deps.Player = rt
	deps.FadeAway = rt.FadeAway
	logPlayer(rt)
	if srv, err := mpris.NewServer(rt); err == nil { // MPRIS is optional
		ch := rt.Subscribe()
		go func() {
			for s := range ch {
				srv.Update(s)
			}
		}()
		defer func() { _ = srv.Close() }()
	}

	var startupErrs []error
	root := cfg.LocalLibrary()
	if st, err := os.Stat(root); err == nil && st.IsDir() {
		scanner := &localscan.Scanner{Root: root}
		deps.ScanLocal = func(ctx context.Context, progress func(done, total int)) (int, error) {
			res, err := scanner.Scan(ctx, store, progress)
			return res.Changed + res.Removed, err
		}
		local, err := mpv.New(ctx, mpv.Options{Resolve: localResolver(ctx, store)})
		if err != nil {
			startupErrs = append(startupErrs, fmt.Errorf("local playback: %w", err))
		} else {
			rt.Attach(router.Local, local)
		}
	}

	var prog *tea.Program
	var (
		mu      sync.Mutex
		stopped bool
		apple   *cdp.Player // the running Apple Music player, or nil
	)
	if !offline {
		// Chrome and MusicKit start when the Apple Music view shows, and shut
		// down after some time in the local view.
		deps.StartApple = func() {
			go func() {
				p, err := startPlayer(ctx, cfg, dt, func(s string) { prog.Send(tui.PlayerStatusMsg(s)) })
				mu.Lock()
				defer mu.Unlock()
				if stopped { // the user quit while it started
					if p != nil {
						_ = p.Close()
					}
					return
				}
				if err != nil {
					go prog.Send(tui.AppleFailedMsg{Err: err})
					return
				}
				apple = p
				rt.Attach(router.Apple, p)
				go prog.Send(tui.PlayerReadyMsg{})
			}()
		}
		deps.StopApple = func() {
			mu.Lock()
			p := apple
			apple = nil
			if p != nil {
				rt.Detach(router.Apple)
			}
			mu.Unlock()
			if p != nil {
				_ = p.Close()
			}
		}
	}

	model := tui.New(ctx, deps)
	prog = tea.NewProgram(model, tea.WithContext(ctx))
	if deps.ScanLocal != nil {
		// New downloads show up by themselves.
		go func() { _ = localscan.Watch(ctx, root, func() { prog.Send(tui.LocalChangedMsg{}) }) }()
	}
	for _, err := range startupErrs {
		go prog.Send(tui.PlayerFailedMsg{Err: err})
	}
	if offline {
		go prog.Send(tui.PlayerStatusMsg("offline: Apple Music is off"))
	}

	_, runErr := prog.Run()
	if deps.Covers != nil {
		_, _ = os.Stdout.WriteString(deps.Covers.Cleanup()) // free the images in the terminal
	}

	mu.Lock()
	stopped = true
	_ = rt.Close()
	mu.Unlock()

	cfg.Volume = model.Volume()
	if err := cfg.Save(); err != nil && runErr == nil {
		runErr = err
	}
	if err := model.Session().Save(sessionPath); err != nil && runErr == nil {
		runErr = err
	}
	if errors.Is(runErr, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return runErr
}

// startPlayer prepares Chrome (downloading it on first run) and loads
// MusicKit.
func startPlayer(ctx context.Context, cfg *config.Config, dt devtoken.Token, status func(string)) (*cdp.Player, error) {
	status("preparing browser…")
	if err := cdp.EnsureBrowser(status); err != nil {
		return nil, fmt.Errorf("browser: %w", err)
	}
	status("starting Apple Music…")
	p, err := cdp.New(cdp.Options{DevToken: dt.Value, UserToken: cfg.UserToken, Origin: dt.Origin, Version: version})
	if err != nil {
		return nil, err
	}
	var saveMu sync.Mutex
	p.OnUserToken = func(t string) {
		saveMu.Lock()
		defer saveMu.Unlock()
		if t != cfg.UserToken {
			cfg.UserToken = t
			_ = cfg.Save()
		}
	}
	wctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := p.WaitReady(wctx); err != nil {
		_ = p.Close()
		return nil, err
	}
	return p, nil
}

// localResolver maps local track ids to what mpv plays.
func localResolver(ctx context.Context, store *library.Store) func(id string) (mpv.Item, error) {
	return func(id string) (mpv.Item, error) {
		t, err := store.LocalTrack(ctx, id)
		if err != nil {
			return mpv.Item{}, err
		}
		it := mpv.Item{ID: t.ID, Path: t.Path, CuePath: t.CuePath, Start: t.Start, Duration: t.Duration,
			Title: t.Title, Artist: t.Artist, Codec: t.Codec, SampleRate: t.SampleRate, Bits: t.Bits, Kbps: t.Kbps, VBR: t.VBR}
		if a, err := store.Album(ctx, t.AlbumID); err == nil {
			it.Album, it.ArtworkURL = a.Title, a.ArtworkURL
		}
		return it, nil
	}
}

// logPlayer appends the player's log lines, errors and skips to
// CacheDir/player.log (recreated each run), for diagnosing playback.
func logPlayer(p interface{ Subscribe() <-chan player.State }) {
	path := filepath.Join(config.CacheDir(), "player.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // our cache dir
	if err != nil {
		return
	}
	ch := p.Subscribe()
	go func() {
		defer func() { _ = f.Close() }()
		for s := range ch {
			var line string
			switch {
			case s.Error != "":
				line = "ERROR " + s.Error
			case s.Log != "":
				line = s.Log
			case s.SkippedID != "":
				line = "skipped " + s.SkippedID
			case s.NeedsAuth:
				line = "needs auth"
			default:
				continue
			}
			_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().Format("15:04:05.000"), line)
		}
	}()
}

func loginWindow(ctx context.Context, dt devtoken.Token) (string, error) {
	fmt.Println("Preparing the browser for Apple Music sign-in…")
	if err := cdp.EnsureBrowser(func(s string) { fmt.Println(" ", s) }); err != nil {
		return "", fmt.Errorf("browser: %w", err)
	}
	fmt.Println("A Chrome window will open music.apple.com.")
	fmt.Println("Click \"Sign In\" (top right) and sign in with your Apple ID;")
	fmt.Println("the window closes by itself once scopolamine sees you are signed in.")
	return cdp.LoginWindow(ctx, dt.Origin, "")
}
