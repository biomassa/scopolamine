// Package auth runs the Apple Music sign-in in the user's default browser:
// a localhost page loads MusicKit JS, calls authorize(), and posts the
// resulting Music-User-Token back to us.
//
// Derived from vibez (https://github.com/simonepelosi/vibez),
// Copyright (c) 2025 Simone Pelosi, MIT License.
package auth

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

//go:embed web/login.html
var loginHTML string

// Page renders the sign-in page for devToken. It posts the user token as
// JSON {"user_token": …} to the relative URL "callback".
func Page(devToken string) (string, error) {
	tmpl, err := template.New("login").Parse(loginHTML)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, map[string]string{"DeveloperToken": devToken}); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Login serves the login page on 127.0.0.1:port, opens it in the default
// browser and returns the user token once the user has signed in.
// say is used for user-facing progress lines.
func Login(ctx context.Context, devToken string, port int, say func(string)) (string, error) {
	page, err := Page(devToken)
	if err != nil {
		return "", err
	}
	tokenCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			UserToken string `json:"user_token"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || body.UserToken == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		select {
		case tokenCh <- body.UserToken:
		default:
		}
	})

	// The URL uses "localhost", which browsers treat as a secure context, as
	// vibez's login page does.
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return "", fmt.Errorf("login server on port %d: %w", port, err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()

	url := fmt.Sprintf("http://localhost:%d/login", port)
	say("Opening your browser to sign in to Apple Music…")
	if err := exec.Command("xdg-open", url).Start(); err != nil { //nolint:gosec // fixed command
		say("Could not open a browser (" + err.Error() + ").")
	}
	say("If nothing opened, visit: " + url)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	select {
	case t := <-tokenCh:
		return t, nil
	case err := <-errCh:
		return "", err
	case <-ctx.Done():
		return "", errors.New("sign-in timed out")
	}
}
