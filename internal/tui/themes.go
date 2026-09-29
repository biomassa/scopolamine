package tui

import (
	"fmt"
	"math"
	"strconv"

	"charm.land/lipgloss/v2"
)

// A palette is a color theme. The "scopolamine" theme has no palette: it
// uses the scopolamine colors on the terminal background. The other
// palettes come from tideui by Allie Bayless
// (github.com/allisonhere/tideui, MIT license), as in godoist.
type palette struct {
	name          string
	bg, fg        string // background and text
	border, focus string // borders and separators; focus is the accent
	selected      string // selected row
	good          string // green (not used by scopolamine yet)
	dimmed        string // secondary text
	errorHex      string // errors
}

// defaultTheme is the name of the default theme.
const defaultTheme = "scopolamine"

// palettes are the themes after "scopolamine", in picker order.
var palettes = []palette{
	{"catppuccin-mocha", "#1e1e2e", "#cdd6f4", "#6c7086", "#89b4fa", "#89b4fa", "#a6e3a1", "#585b70", "#f38ba8"},
	{"catppuccin-latte", "#eff1f5", "#4c4f69", "#9ca0b0", "#1e66f5", "#1e66f5", "#40a02b", "#8c8fa1", "#d20f39"},
	{"catppuccin-frappe", "#303446", "#c6d0f5", "#626880", "#8caaee", "#8caaee", "#a6d189", "#51576d", "#e78284"},
	{"catppuccin-macchiato", "#24273a", "#cad3f5", "#5b6078", "#8aadf4", "#8aadf4", "#a6da95", "#494d64", "#ed8796"},
	{"nord", "#2e3440", "#eceff4", "#4c566a", "#88c0d0", "#88c0d0", "#a3be8c", "#4c566a", "#bf616a"},
	{"dracula", "#282a36", "#f8f8f2", "#6272a4", "#bd93f9", "#bd93f9", "#50fa7b", "#6272a4", "#ff5555"},
	{"gruvbox-dark", "#282828", "#ebdbb2", "#504945", "#83a598", "#83a598", "#b8bb26", "#504945", "#fb4934"},
	{"gruvbox-light", "#fbf1c7", "#3c3836", "#bdae93", "#076678", "#076678", "#79740e", "#bdae93", "#cc241d"},
	{"tokyo-night", "#1a1b26", "#c0caf5", "#414868", "#7aa2f7", "#7aa2f7", "#9ece6a", "#414868", "#f7768e"},
	{"tokyo-night-day", "#e1e2e7", "#3760bf", "#a8aecb", "#2e7de9", "#2e7de9", "#587539", "#a8aecb", "#f52a65"},
	{"rose-pine", "#191724", "#e0def4", "#403d52", "#c4a7e7", "#c4a7e7", "#9ccfd8", "#403d52", "#eb6f92"},
	{"rose-pine-moon", "#232136", "#e0def4", "#44415a", "#c4a7e7", "#c4a7e7", "#9ccfd8", "#44415a", "#eb6f92"},
	{"rose-pine-dawn", "#faf4ed", "#575279", "#d7d2be", "#907aa9", "#907aa9", "#286983", "#d7d2be", "#b4637a"},
	{"one-dark", "#282c34", "#abb2bf", "#3e4451", "#61afef", "#61afef", "#98c379", "#3e4451", "#e06c75"},
	{"magenta-geode", "#47003c", "#f3b0dc", "#aa4d84", "#c83fa9", "#c83fa9", "#f3b0dc", "#77176e", "#ff7062"},
	{"coral-sunset", "#444154", "#fec9c1", "#fc8b79", "#ff7062", "#ff7062", "#fec9c1", "#7a637f", "#ff7062"},
	{"lavender-fields-forever", "#382d72", "#e5ccf4", "#b7c2c6", "#a080e1", "#a080e1", "#e5ccf4", "#5c509c", "#ff7062"},
	{"vt100", "#000000", "#33ff33", "#145214", "#00ff00", "#00ff00", "#66ff66", "#3dcc3d", "#ff6b6b"},
	{"vt52", "#000000", "#ffcc66", "#6b4e14", "#ffb020", "#ffb020", "#ffe6a8", "#a67c2e", "#ff6666"},
}

// ThemeNames returns all theme names, "scopolamine" first. The CLI uses it
// for --theme.
func ThemeNames() []string {
	names := []string{defaultTheme}
	for _, p := range palettes {
		names = append(names, p.name)
	}
	return names
}

// ValidTheme reports whether name is a theme.
func ValidTheme(name string) bool {
	for _, n := range ThemeNames() {
		if n == name {
			return true
		}
	}
	return false
}

// The theme in use. themeBg is its background, or "" for the terminal
// background ("scopolamine" theme).
var (
	themeName = defaultTheme
	themeBg   string
)

// The colors of the theme in use.
var (
	hexAccent, hexText, hexDim, hexSelBg, hexSelText, hexSep, hexErr string

	stTitle, stTitleFocus, stRow, stDim, stSel, stSelFocus lipgloss.Style
	stPlaying, stSep, stErr, stBold, stHeader              lipgloss.Style
)

func init() { ApplyTheme(defaultTheme) }

// ApplyTheme sets the colors of theme name. An unknown name gives the
// "scopolamine" theme.
func ApplyTheme(name string) {
	themeName, themeBg = defaultTheme, ""
	hexAccent, hexText, hexDim = "#d7af5f", "#d0d0d0", "#6c6c6c"
	hexSelBg, hexSelText, hexSep, hexErr = "#3a3a3a", "#1c1c1c", "#444444", "#e06c75"
	for _, p := range palettes {
		if p.name == name {
			applyPalette(p)
			break
		}
	}
	buildStyles()
}

// applyPalette maps a palette onto the scopolamine colors: the accent is the
// focus color of the theme.
func applyPalette(p palette) {
	themeName, themeBg = p.name, p.bg
	hexAccent = readable(p.focus, p.bg, 3)
	hexText = p.fg
	hexDim = readable(p.dimmed, p.bg, 3)
	hexSelBg = mix(p.selected, p.bg, 0.72)
	hexSep = p.border
	hexErr = readable(p.errorHex, p.bg, 3)
	// The text on the accent-colored cursor row: the background or the text
	// color of the theme, whichever is easier to read.
	hexSelText = p.bg
	if contrast(p.fg, hexAccent) > contrast(p.bg, hexAccent) {
		hexSelText = p.fg
	}
}

func buildStyles() {
	c := func(h string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(h)) }
	stTitle = c(hexDim).Bold(true)
	stTitleFocus = c(hexAccent).Bold(true)
	stRow = c(hexText)
	stDim = c(hexDim)
	stSel = c(hexText).Background(lipgloss.Color(hexSelBg))
	stSelFocus = c(hexSelText).Background(lipgloss.Color(hexAccent)).Bold(true)
	stPlaying = c(hexAccent)
	stSep = c(hexSep)
	stErr = c(hexErr)
	stBold = c(hexText).Bold(true)
	stHeader = c(hexAccent).Bold(true)
}

// themeLabel is the name shown in the picker.
func themeLabel(name string) string {
	if name == defaultTheme {
		return fmt.Sprintf("%s (terminal background)", name)
	}
	return name
}

// parseHex splits "#RRGGBB" into its components. It returns gray for a bad
// value.
func parseHex(h string) (r, g, b float64) {
	if len(h) != 7 {
		return 128, 128, 128
	}
	v, err := strconv.ParseUint(h[1:], 16, 32)
	if err != nil {
		return 128, 128, 128
	}
	return float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff)
}

// mix blends a toward b by t (0 = a, 1 = b).
func mix(a, b string, t float64) string {
	ar, ag, ab := parseHex(a)
	br, bg, bb := parseHex(b)
	l := func(x, y float64) int { return int(math.Round(x + (y-x)*t)) }
	return fmt.Sprintf("#%02X%02X%02X", l(ar, br), l(ag, bg), l(ab, bb))
}

// luminance is the relative brightness of a color, from 0 to 1.
func luminance(h string) float64 {
	r, g, b := parseHex(h)
	return (0.2126*r + 0.7152*g + 0.0722*b) / 255
}

// contrast is the WCAG contrast ratio of two colors, from 1 to 21.
func contrast(a, b string) float64 {
	la, lb := relLum(a), relLum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// relLum is the WCAG relative luminance of a color.
func relLum(h string) float64 {
	r, g, b := parseHex(h)
	lin := func(v float64) float64 {
		v /= 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// readable moves h toward white or black (away from bg) until it has the
// contrast min against bg. It keeps the hue.
func readable(h, bg string, min float64) string {
	to := "#FFFFFF"
	if luminance(bg) > 0.5 {
		to = "#000000"
	}
	out := h
	for t := 0.1; contrast(out, bg) < min && t <= 1; t += 0.1 {
		out = mix(h, to, t)
	}
	return out
}
