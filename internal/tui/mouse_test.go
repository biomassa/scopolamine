package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// at finds text on the screen: the column of its first cell and its line.
// nth picks the nth occurrence (0 = first).
func at(t *testing.T, m *Model, text string, nth int) (x, y int) {
	t.Helper()
	for i, l := range strings.Split(screen(m), "\n") {
		from := 0
		for {
			j := strings.Index(l[from:], text)
			if j < 0 {
				break
			}
			if nth == 0 {
				return ansi.StringWidth(l[:from+j]), i
			}
			nth--
			from += j + len(text)
		}
	}
	t.Fatalf("%q not on the screen:\n%s", text, screen(m))
	return 0, 0
}

func click(m *Model, x, y int) {
	_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	drive(m, cmd)
}

func wheel(m *Model, x, y int, down bool) {
	b := tea.MouseWheelUp
	if down {
		b = tea.MouseWheelDown
	}
	_, cmd := m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: b})
	drive(m, cmd)
}

func TestMouse(t *testing.T) {
	fp := &fakePlayer{}
	m := New(context.Background(), Deps{Store: fixtureStore(t), Player: fp, Volume: 0.5})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	drive(m, m.Init())

	// A click selects a row and focuses its column.
	bx, by := at(t, m, "Boards of Canada", 0)
	click(m, bx, by)
	if m.selectedArtist() != "Boards of Canada" || m.focus != paneArtists {
		t.Fatalf("artist %q, focus %d", m.selectedArtist(), m.focus)
	}
	x, y := at(t, m, "2002  Geogaddi", 0)
	click(m, x, y)
	if a, ok := m.selectedAlbum(); !ok || a.ID != "l.a" || m.focus != paneAlbums || fp.ids != nil {
		t.Fatalf("album %+v, focus %d, ids %v", a, m.focus, fp.ids)
	}

	// A double click plays the album.
	click(m, x, y)
	if strings.Join(fp.ids, ",") != "i.a1,i.a2" {
		t.Fatalf("double click: PlayTracks(%v)", fp.ids)
	}
	playing(m, "i.a1", "Ready Lets Go", 30*time.Second)

	// A double click on the row that holds the playing track pauses.
	m.click = lastClick{}
	key(m, "2")
	x, y = at(t, m, "2002  Geogaddi", 0)
	click(m, x, y)
	click(m, x, y)
	if fp.toggle != 1 || len(fp.ids) != 2 {
		t.Fatalf("double click on the playing album: toggles %d, ids %v", fp.toggle, fp.ids)
	}

	// Two clicks far apart in time are two single clicks.
	m.click.at = time.Now().Add(-time.Second)
	click(m, x, y)
	if fp.toggle != 1 {
		t.Fatal("slow clicks made a double click")
	}

	// Legend: each symbol of "[ ]" is its own key.
	lx, ly := at(t, m, "[ ]", 0)
	click(m, lx+2, ly)
	click(m, lx, ly)
	click(m, lx+1, ly) // the space between: nothing
	if fp.next != 1 || fp.prev != 1 {
		t.Fatalf("legend [ ]: next %d, prev %d", fp.next, fp.prev)
	}
	lx, ly = at(t, m, "+/-", 0)
	click(m, lx, ly)
	if m.volume < 0.54 {
		t.Fatalf("legend +: volume %v", m.volume)
	}

	// The progress bar seeks to the click.
	b := m.hits.bar
	click(m, b.x0+(b.x1-b.x0)/2, b.y)
	if len(fp.seeks) != 1 || fp.seeks[0] < 2*time.Minute || fp.seeks[0] > 3*time.Minute+30*time.Second {
		t.Fatalf("bar seek: %v (track 5:21)", fp.seeks)
	}

	// The wheel moves the cursor of the column under the mouse.
	ax, ay := at(t, m, "Broadcast", 0)
	wheel(m, ax, ay, false) // up: from Boards of Canada to All artists
	if m.selectedArtist() != allArtists || m.focus != paneArtists {
		t.Fatalf("wheel: artist %q, focus %d", m.selectedArtist(), m.focus)
	}

	// The theme picker: a click previews, a click outside cancels.
	orig := themeName
	key(m, "T")
	tx, tyy := at(t, m, "nord", 0)
	click(m, tx, tyy)
	if themeName != "nord" || m.themes == nil {
		t.Fatalf("theme click: %s", themeName)
	}
	click(m, 2, 10)
	if m.themes != nil || themeName != orig {
		t.Fatalf("outside click: picker %v, theme %s", m.themes != nil, themeName)
	}

	// A click on the title of a filtered column edits the filter again.
	key(m, "1")
	key(m, "/")
	typeText(m, "bro")
	ax, ay = at(t, m, "Broadcast", 0)
	click(m, ax, ay)
	if m.filtering || m.panes[paneArtists].filter != "bro" {
		t.Fatalf("click in the column: filtering %v, filter %q", m.filtering, m.panes[paneArtists].filter)
	}
	fx, fy := at(t, m, "/bro", 0)
	click(m, fx, fy)
	if !m.filtering {
		t.Fatal("a click on the filtered title did not edit the filter")
	}
	key(m, "esc")

	// Any click closes the help.
	key(m, "?")
	click(m, 5, 5)
	if m.showHelp {
		t.Fatal("click did not close the help")
	}
}

func TestMouseSearchAndFolder(t *testing.T) {
	fp := &fakePlayer{}
	cat := &fakeCatalog{}
	m := New(context.Background(), Deps{Store: localStore(t), Player: fp, Catalog: cat,
		ScanLocal:    func(context.Context, func(int, int)) (int, error) { return 0, nil },
		SetLocalRoot: func(string) error { return nil }})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	drive(m, m.Init())

	// Search: a click on the artist shows its albums; a double click plays.
	key(m, "s")
	typeText(m, "loscil")
	key(m, "enter")
	x, y := at(t, m, "Loscil", 1) // the artist row (0 is in "Matching albums" rows)
	click(m, x, y)
	if m.search.albumsFor != "art1" || m.search.focus != paneArtists {
		t.Fatalf("artist click: albums for %q, focus %d", m.search.albumsFor, m.search.focus)
	}
	x, y = at(t, m, "2002  Submers", 0)
	click(m, x, y)
	click(m, x, y)
	if strings.Join(fp.ids, ",") != "2001,2002" {
		t.Fatalf("search double click: PlayTracks(%v)", fp.ids)
	}

	// A click on the search line edits the search text again.
	key(m, "s")
	if !m.search.editing {
		t.Fatal("s did not edit")
	}
	click(m, x, y) // a click in a column ends the editing
	if m.search.editing {
		t.Fatal("a click in a column did not end the editing")
	}
	click(m, 30, 0)
	if !m.search.editing {
		t.Fatal("a click on the search line did not edit again")
	}
	key(m, "enter")

	// The folder box: a click on a match fills it in; outside closes.
	key(m, "esc")
	key(m, "L") // no folder: the box
	m.folder.text, m.folder.parent, m.folder.cands, m.folder.cur = "/m/c", "/m/", []string{"complete", "completed-old"}, -1
	fx, fy := at(t, m, "completed-old/", 0)
	click(m, fx+2, fy)
	if m.folder.text != "/m/completed-old/" {
		t.Fatalf("folder match click: %q", m.folder.text)
	}
	click(m, 1, 20)
	if m.folder != nil {
		t.Fatal("outside click did not close the folder box")
	}
}
