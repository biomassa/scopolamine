package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The theme picker (T) lists the themes at the top right of the screen, as
// in godoist. A move previews the highlighted theme on the whole screen.
// enter keeps the theme and saves it in the config file. esc goes back to
// the theme that was in use.

type themePicker struct {
	names []string
	cur   int
	orig  string // the theme in use when the picker opened
}

type themeSavedMsg struct {
	name string
	err  error
}

const themePickerWidth = 40

func (m *Model) openThemePicker() {
	names := ThemeNames()
	cur := 0
	for i, n := range names {
		if n == themeName {
			cur = i
		}
	}
	m.themes = &themePicker{names: names, cur: cur, orig: themeName}
}

func (m *Model) handleThemeKey(k string) tea.Cmd {
	p := m.themes
	switch k {
	case "up", "k":
		p.cur = (p.cur - 1 + len(p.names)) % len(p.names)
	case "down", "j":
		p.cur = (p.cur + 1) % len(p.names)
	case "home", "g":
		p.cur = 0
	case "end", "G":
		p.cur = len(p.names) - 1
	case "enter":
		name := p.names[p.cur]
		m.themes = nil
		ApplyTheme(name)
		save := m.deps.SaveTheme
		if save == nil {
			return m.flash("theme "+name, false)
		}
		return func() tea.Msg { return themeSavedMsg{name: name, err: save(name)} }
	case "esc", "T", "q":
		m.themes = nil
		ApplyTheme(p.orig)
		return m.flash("theme "+p.orig, false)
	default:
		return nil
	}
	ApplyTheme(p.names[p.cur])
	return nil
}

func (m *Model) onThemeSaved(msg themeSavedMsg) tea.Cmd {
	if msg.err != nil {
		return m.flash("theme "+msg.name+" is in use, but it was not saved: "+msg.err.Error(), true)
	}
	return m.flash("theme "+msg.name+" · saved for the next start", false)
}

// themeBox draws the picker: a border, a space, the list, a space, the hint.
func (m *Model) themeBox() (lines []string, width int) {
	p := m.themes
	w := min(themePickerWidth, max(20, m.width-4))
	h := min(len(p.names)+5, m.height-3)
	inner := w - 2
	rows := h - 5
	off := max(0, min(p.cur-rows/2, len(p.names)-rows))
	body := []string{strings.Repeat(" ", inner)}
	for i := off; i < len(p.names) && i < off+rows; i++ {
		label := themeLabel(p.names[i])
		if i == p.cur {
			body = append(body, stSelFocus.Render(fit(" ▸ "+label, inner)))
			continue
		}
		body = append(body, stRow.Render(fit("   "+label, inner)))
	}
	body = append(body, strings.Repeat(" ", inner), stDim.Render(fit(" ↑/↓ preview · enter keep · esc back", inner)))
	border := stTitleFocus
	title := " Theme "
	top := border.Render("╭─") + stTitleFocus.Render(title) + border.Render(strings.Repeat("─", max(0, inner-1-ansi.StringWidth(title)))+"╮")
	lines = append(lines, top)
	for _, b := range body {
		lines = append(lines, border.Render("│")+b+border.Render("│"))
	}
	lines = append(lines, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return lines, w
}

// overlay draws box over the lines of screen, with its top left corner at
// column x and row y.
func overlay(screen string, box []string, x, y int) string {
	lines := strings.Split(screen, "\n")
	for i, b := range box {
		row := y + i
		if row >= len(lines) {
			break
		}
		base := lines[row]
		left := ansi.Truncate(base, x, "")
		if pad := x - ansi.StringWidth(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := ansi.TruncateLeft(base, x+ansi.StringWidth(b), "")
		lines[row] = left + "\x1b[m" + b + "\x1b[m" + right
	}
	return strings.Join(lines, "\n")
}
