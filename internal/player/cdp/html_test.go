//go:build linux

package cdp

import (
	"strings"
	"testing"
)

func TestRenderHTML(t *testing.T) {
	h, err := renderHTML("eyJ.dev.tok", "0.user+tok/=", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"developerToken: 'eyJ.dev.tok'", "music.musicUserToken = '0.user+tok/='", "window.scPlayTracks", "musickit/v3/musickit.js"} {
		if !strings.Contains(h, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
	if _, err := renderHTML("x'; alert(1); '", "u", "1"); err == nil {
		t.Error("token with a quote was accepted")
	}
}
