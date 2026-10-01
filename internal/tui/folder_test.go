package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestFolderCompletion(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"complete", "completed-old", "music", ".hidden"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "compfile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p := &folderPrompt{text: dir + "/comp", cur: -1}
	p.complete() // the common part
	if p.text != dir+"/complete" || len(p.cands) != 2 {
		t.Fatalf("first tab: %q %v", p.text, p.cands)
	}
	p.complete() // cycle
	if p.text != dir+"/complete/" {
		t.Fatalf("second tab: %q", p.text)
	}
	p.complete()
	if p.text != dir+"/completed-old/" {
		t.Fatalf("third tab: %q", p.text)
	}
	p = &folderPrompt{text: dir + "/m", cur: -1}
	p.complete() // one match
	if p.text != dir+"/music/" || p.cands != nil {
		t.Fatalf("one match: %q %v", p.text, p.cands)
	}
	p = &folderPrompt{text: dir + "/", cur: -1}
	p.complete()
	if strings.Contains(strings.Join(p.cands, ","), ".hidden") {
		t.Fatalf("hidden folder offered: %v", p.cands)
	}
}

func TestFolderBox(t *testing.T) {
	dir := t.TempDir()
	music := filepath.Join(dir, "music")
	if err := os.Mkdir(music, 0o755); err != nil {
		t.Fatal(err)
	}
	var set []string
	fp := &fakePlayer{}
	m := New(context.Background(), Deps{Store: localStore(t), Player: fp, DefaultLocalRoot: music,
		ScanLocal:    func(context.Context, func(int, int)) (int, error) { return 0, nil },
		SetLocalRoot: func(p string) error { set = append(set, p); return nil }})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	drive(m, m.Init())

	// No folder yet: L opens the box.
	key(m, "L")
	if m.folder == nil || !strings.Contains(screen(m), "Local music folder") {
		t.Fatalf("L without a folder:\n%s", screen(m))
	}
	typeText(m, dir+"/nope")
	key(m, "enter")
	if len(set) != 0 || !strings.Contains(screen(m), "not a folder") {
		t.Fatalf("bad folder: set = %v\n%s", set, screen(m))
	}
	key(m, "esc")
	if m.folder != nil || m.source != "apple" {
		t.Fatal("esc did not close the box")
	}

	// An empty input saves the default folder and opens the local mode.
	key(m, "L")
	for range len([]rune(m.folder.text)) {
		key(m, "backspace")
	}
	key(m, "enter")
	if len(set) != 1 || set[0] != music || m.folder != nil || m.localRoot != music {
		t.Fatalf("empty input: set = %v, root = %q", set, m.localRoot)
	}
	m.notice = "" // the scan notice covers the legend
	if !strings.Contains(screen(m), "F folder") {
		t.Fatalf("no F in the local legend:\n%s", screen(m))
	}

	// F opens the box with the folder in it.
	key(m, "F")
	if m.folder == nil || m.folder.text != music {
		t.Fatalf("F: %+v", m.folder)
	}
}

func TestFolderCursor(t *testing.T) {
	m := New(context.Background(), Deps{Store: localStore(t), Player: &fakePlayer{},
		ScanLocal:    func(context.Context, func(int, int)) (int, error) { return 0, nil },
		SetLocalRoot: func(string) error { return nil }})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	drive(m, m.Init())
	key(m, "L")
	typeText(m, "/mnt/msic")
	key(m, "left")
	key(m, "left")
	key(m, "left")
	typeText(m, "u")
	if m.folder.text != "/mnt/music" {
		t.Fatalf("insert: %q", m.folder.text)
	}
	key(m, "right")
	key(m, "backspace")
	if m.folder.text != "/mnt/muic" || !strings.Contains(screen(m), "/mnt/mu│ic") {
		t.Fatalf("backspace: %q\n%s", m.folder.text, screen(m))
	}
}
