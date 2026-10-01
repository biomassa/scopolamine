package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/biomassa/scopolamine/internal/library"
	"github.com/biomassa/scopolamine/internal/player"
)

const folderBoxWidth = 64

// folderPrompt is the open box for the local music folder.
type folderPrompt struct {
	text   string   // the path as typed; "~" stands for the home folder
	pos    int      // the cursor, in runes from the start of text
	cands  []string // tab: the matching folder names, or nil
	cur    int      // tab: the name of cands in text, or -1
	parent string   // tab: text up to and with the last "/"
	err    string
	busy   bool // the folder is being set
}

// localRootSetMsg reports the result of SetLocalRoot.
type localRootSetMsg struct {
	path string
	err  error
}

// localAvailable reports whether the local mode has a folder.
func (m *Model) localAvailable() bool {
	if m.deps.ScanLocal == nil {
		return false
	}
	return m.localRoot != "" || m.deps.SetLocalRoot == nil
}

// openFolderPrompt opens the box, with the current folder in it.
func (m *Model) openFolderPrompt() {
	t := tildePath(m.localRoot)
	m.folder = &folderPrompt{text: t, pos: len([]rune(t)), cur: -1}
}

func (m *Model) handleFolderKey(msg tea.KeyPressMsg, k string) tea.Cmd {
	p := m.folder
	if p.busy {
		return nil
	}
	if k != "tab" {
		p.cands, p.cur = nil, -1
	}
	switch k {
	case "esc":
		m.folder = nil
		return nil
	case "enter":
		path := expandTilde(strings.TrimSpace(p.text))
		if path == "" {
			path = m.deps.DefaultLocalRoot
		}
		path = filepath.Clean(path)
		if st, err := os.Stat(path); err != nil || !st.IsDir() {
			p.err = "not a folder: " + tildePath(path)
			return nil
		}
		p.err, p.busy = "", true
		set := m.deps.SetLocalRoot
		return func() tea.Msg { return localRootSetMsg{path: path, err: set(path)} }
	case "tab":
		p.err = ""
		p.complete()
		p.pos = len([]rune(p.text))
		return nil
	case "backspace":
		p.err = ""
		if r := []rune(p.text); p.pos > 0 {
			p.text = string(r[:p.pos-1]) + string(r[p.pos:])
			p.pos--
		}
		return nil
	case "left":
		p.pos = max(0, p.pos-1)
		return nil
	case "right":
		p.pos = min(len([]rune(p.text)), p.pos+1)
		return nil
	}
	t := msg.Text
	if t == "" && k == "space" {
		t = " "
	}
	if t == "" || strings.IndexFunc(t, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return nil
	}
	p.err = ""
	r := []rune(p.text)
	p.text = string(r[:p.pos]) + t + string(r[p.pos:])
	p.pos += len([]rune(t))
	return nil
}

// complete is tab: the first push completes the common part of the matching
// folder names; the next pushes cycle through them.
func (p *folderPrompt) complete() {
	if p.cands != nil {
		p.cur = (p.cur + 1) % len(p.cands)
		p.text = p.parent + p.cands[p.cur] + "/"
		return
	}
	parent, base := p.text, ""
	if i := strings.LastIndexByte(p.text, '/'); i >= 0 {
		parent, base = p.text[:i+1], p.text[i+1:]
	} else {
		parent, base = "", p.text
	}
	dir := expandTilde(parent)
	if dir == "" {
		dir = "."
	}
	names := dirNames(dir, base)
	switch len(names) {
	case 0:
		return
	case 1:
		p.text = parent + names[0] + "/"
		return
	}
	p.parent, p.cands, p.cur = parent, names, -1
	if cp := commonPrefix(names); len(cp) > len(base) {
		p.text = parent + cp
		return
	}
	p.cur = 0
	p.text = parent + names[0] + "/"
}

// dirNames lists the folders in dir whose names start with base. Hidden
// folders show only when base starts with a dot.
func dirNames(dir, base string) []string {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range des {
		n := d.Name()
		if !strings.HasPrefix(n, base) || (strings.HasPrefix(n, ".") && !strings.HasPrefix(base, ".")) {
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil && st.IsDir() { // follows links
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

func commonPrefix(names []string) string {
	cp := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, cp) {
			cp = cp[:len(cp)-1]
		}
	}
	return cp
}

func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}

func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || p == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

// onLocalRootSet starts over with the new folder: the old folder's music
// stops, and the local mode forgets its selection and resume point.
func (m *Model) onLocalRootSet(msg localRootSetMsg) tea.Cmd {
	p := m.folder
	if msg.err != nil {
		if p != nil {
			p.busy, p.err = false, msg.err.Error()
		}
		return nil
	}
	m.folder = nil
	m.localRoot = msg.path
	var cmds []tea.Cmd
	if m.state.Track != nil && m.state.Local {
		m.playingAlbum, m.queueAlbums = library.Album{}, nil
		cmds = append(cmds, m.withPlayer(player.Player.Stop))
	}
	folders := m.local.folders
	fresh := newLibView(library.SourceLocal)
	fresh.folders = folders
	if m.libView == m.local {
		m.libView = fresh
	}
	m.local = fresh
	m.syncing = false // a scan of the old folder may still run; its result is old
	cmds = append(cmds, m.showView(m.local), m.flash("local folder: "+tildePath(msg.path)+" · scanning…", false), m.startSync())
	return tea.Batch(cmds...)
}

// folderBox draws the box: a border, a space, the path, the matches or the
// error, a space, the hint.
func (m *Model) folderBox() (lines []string, width int) {
	p := m.folder
	w := min(folderBoxWidth, max(24, m.width-4))
	inner := w - 2
	pathLine := stBold.Render(fit(" "+p.window(inner-3), inner))
	if p.busy {
		pathLine = stDim.Render(fit(" "+p.text, inner))
	}
	var info string
	switch {
	case p.err != "":
		info = stErr.Render(fit(" "+p.err, inner))
	case p.cands != nil:
		var parts []string
		for i, c := range p.cands {
			if i == p.cur {
				parts = append(parts, stTitleFocus.Render(c+"/"))
			} else {
				parts = append(parts, stDim.Render(c+"/"))
			}
		}
		info = fit(" "+strings.Join(parts, "  "), inner)
	default:
		info = strings.Repeat(" ", inner)
	}
	hint := " tab complete · enter save · esc cancel · empty: " + tildePath(m.deps.DefaultLocalRoot)
	body := []string{strings.Repeat(" ", inner), pathLine, info, strings.Repeat(" ", inner), stDim.Render(fit(hint, inner))}
	border := stTitleFocus
	title := " Local music folder "
	top := border.Render("╭─") + stTitleFocus.Render(title) + border.Render(strings.Repeat("─", max(0, inner-1-ansi.StringWidth(title)))+"╮")
	lines = append(lines, top)
	for _, b := range body {
		lines = append(lines, border.Render("│")+b+border.Render("│"))
	}
	lines = append(lines, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return lines, w
}

// window is the path with the cursor, cut to w columns around the cursor.
func (p *folderPrompt) window(w int) string {
	r := []rune(p.text)
	before, after := string(r[:p.pos]), string(r[p.pos:])
	if bw := ansi.StringWidth(before); bw > w-1 { // keep the cursor in sight
		before = "…" + ansi.TruncateLeft(before, bw-w+2, "")
	}
	line := before + "│" + after
	if ansi.StringWidth(line) > w {
		line = ansi.Truncate(line, w, "…")
	}
	return line
}
