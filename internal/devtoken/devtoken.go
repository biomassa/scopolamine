// Package devtoken supplies the Apple Music developer token (a signed JWT that
// identifies the calling app). Every Apple Music API request and MusicKit JS
// instance needs one, in addition to the per-user Music-User-Token.
//
// Sources are tried in order; the first one that yields a token wins. New
// sources (e.g. signing with your own MusicKit key) plug in behind Source.
package devtoken

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// EnvVar is the environment variable checked by Env.
const EnvVar = "SCOPOLAMINE_DEV_TOKEN"

// ErrNoToken means a source has nothing to offer; the chain moves on.
var ErrNoToken = errors.New("no developer token")

// Token is a developer token plus the web origin Apple requires requests
// made with it to come from ("" when unrestricted).
type Token struct {
	Value  string
	Origin string
}

// Source yields a developer token.
type Source interface {
	Name() string
	Token(ctx context.Context) (Token, error)
}

// Static returns a fixed token, e.g. from the config file.
type Static struct {
	Label string
	Value string
}

func (s Static) Name() string { return s.Label }

func (s Static) Token(context.Context) (Token, error) {
	return fromString(s.Value)
}

// Env reads EnvVar.
type Env struct{}

func (Env) Name() string { return "$" + EnvVar }

func (Env) Token(context.Context) (Token, error) {
	return fromString(os.Getenv(EnvVar))
}

func fromString(v string) (Token, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return Token{}, ErrNoToken
	}
	return Token{Value: v, Origin: OriginFor(v)}, nil
}

// Chain tries each source in order.
type Chain []Source

// Resolve returns the first usable token and the name of the source it came
// from. Tokens that are well-formed JWTs and already expired are skipped;
// a failing source is reported but does not stop the chain.
func (c Chain) Resolve(ctx context.Context) (Token, string, error) {
	var tried []string
	for _, s := range c {
		t, err := s.Token(ctx)
		switch {
		case errors.Is(err, ErrNoToken):
			tried = append(tried, s.Name())
			continue
		case err != nil:
			tried = append(tried, fmt.Sprintf("%s (%v)", s.Name(), err))
			continue
		}
		if c := parseClaims(t.Value); c != nil && c.Exp != 0 && time.Now().After(time.Unix(c.Exp, 0)) {
			tried = append(tried, s.Name()+" (expired "+time.Unix(c.Exp, 0).Format("2006-01-02")+")")
			continue
		}
		return t, s.Name(), nil
	}
	return Token{}, "", fmt.Errorf("%w; tried: %s", ErrNoToken, strings.Join(tried, ", "))
}

type header struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type claims struct {
	Iss             string   `json:"iss"`
	Exp             int64    `json:"exp"`
	RootHTTPSOrigin []string `json:"root_https_origin"`
}

func decodeSegment(seg string, dst any) bool {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(seg, "="))
	return err == nil && json.Unmarshal(b, dst) == nil
}

func parseHeader(token string) *header {
	parts := strings.Split(token, ".")
	var h header
	if len(parts) != 3 || !decodeSegment(parts[0], &h) {
		return nil
	}
	return &h
}

func parseClaims(token string) *claims {
	parts := strings.Split(token, ".")
	var c claims
	if len(parts) != 3 || !decodeSegment(parts[1], &c) {
		return nil
	}
	return &c
}

// Expiry decodes the JWT "exp" claim without verifying the signature. ok is
// false when the token is not a readable JWT.
func Expiry(token string) (time.Time, bool) {
	c := parseClaims(token)
	if c == nil || c.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(c.Exp, 0), true
}

// OriginFor returns the origin requests must carry for tokens restricted with
// a root_https_origin claim (e.g. ["apple.com"] → https://music.apple.com).
func OriginFor(token string) string {
	c := parseClaims(token)
	if c == nil || len(c.RootHTTPSOrigin) == 0 {
		return ""
	}
	root := strings.TrimPrefix(c.RootHTTPSOrigin[0], "https://")
	if root == "apple.com" {
		return "https://music.apple.com"
	}
	return "https://" + root
}
