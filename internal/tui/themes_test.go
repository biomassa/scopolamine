package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestThemePicker(t *testing.T) {
	defer ApplyTheme(defaultTheme)
	var saved string
	m := New(context.Background(), Deps{Store: fixtureStore(t), SaveTheme: func(n string) error { saved = n; return nil }})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	drive(m, m.Init())

	if m.View().BackgroundColor != nil {
		t.Fatal("the default theme must keep the terminal background")
	}
	key(m, "T")
	if m.themes == nil || !strings.Contains(ansi.Strip(m.View().Content), "▸ scopolamine (terminal background)") {
		t.Fatalf("picker not open:\n%s", ansi.Strip(m.View().Content))
	}
	for _, l := range strings.Split(m.View().Content, "\n") {
		if w := ansi.StringWidth(l); w != 120 && w != 0 {
			// Every screen line keeps the terminal width with the overlay.
			if w > 120 {
				t.Fatalf("overlay line too wide (%d): %q", w, ansi.Strip(l))
			}
		}
	}

	key(m, "j") // preview catppuccin-mocha
	if themeName != "catppuccin-mocha" || m.View().BackgroundColor == nil {
		t.Fatalf("no live preview: theme=%s", themeName)
	}
	key(m, "esc")
	if themeName != defaultTheme || m.themes != nil || saved != "" {
		t.Fatal("esc did not restore the theme")
	}

	key(m, "T")
	key(m, "j")
	key(m, "j") // catppuccin-latte
	key(m, "enter")
	if themeName != "catppuccin-latte" || saved != "catppuccin-latte" || m.themes != nil {
		t.Fatalf("enter: theme=%s saved=%s", themeName, saved)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "saved for the next start") {
		t.Fatal("no save confirmation")
	}
}

func TestPalettesReadable(t *testing.T) {
	defer ApplyTheme(defaultTheme)
	for _, p := range palettes {
		ApplyTheme(p.name)
		if themeName != p.name || themeBg != p.bg {
			t.Fatalf("%s not applied", p.name)
		}
		for role, h := range map[string]string{"text": hexText, "accent": hexAccent, "dim": hexDim, "error": hexErr} {
			if min := map[string]float64{"text": 3, "accent": 3, "dim": 3, "error": 3}[role]; contrast(h, p.bg) < min-0.01 {
				t.Errorf("%s: %s %s has contrast %.2f on %s", p.name, role, h, contrast(h, p.bg), p.bg)
			}
		}
		if contrast(hexSelText, hexAccent) < 1.5 {
			t.Errorf("%s: cursor row text unreadable on the accent", p.name)
		}
	}
	ApplyTheme("no-such-theme")
	if themeName != defaultTheme {
		t.Fatal("unknown theme did not fall back")
	}
}
