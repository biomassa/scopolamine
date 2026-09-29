package tui

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/biomassa/scopolamine/internal/cover"
	"github.com/biomassa/scopolamine/internal/library"
)

const placeholderRune = "\U0010EEEE"

func TestCovers(t *testing.T) {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 64)), nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(buf.Bytes()) }))
	defer srv.Close()

	ctx := context.Background()
	store, err := library.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.ReplaceAlbums(ctx, library.SourceApple, []library.Album{
		{ID: "l.a", Title: "Geogaddi", Artist: "Boards of Canada", ReleaseDate: "2002", ArtworkURL: srv.URL + "/a.jpg"},
		{ID: "l.b", Title: "Campfire Headphase", Artist: "Boards of Canada", ReleaseDate: "2005", ArtworkURL: srv.URL + "/b.jpg"},
		{ID: "l.n", Title: "No Art", Artist: "Broadcast", ReleaseDate: "2003"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"l.a", "l.b", "l.n"} {
		if err := store.SetTracks(ctx, id, []library.Track{{ID: "i." + id, Title: "Track " + id, Disc: 1, Number: 1, Duration: time.Minute, Playable: true}}); err != nil {
			t.Fatal(err)
		}
	}

	covers := cover.New(t.TempDir(), 10, 20)
	m := New(ctx, Deps{Store: store, Player: &fakePlayer{}, Covers: covers, Catalog: &fakeCatalog{}})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	covers.CellW, covers.CellH = 10, 20 // the test has no terminal to ask

	var raws []string
	run := func(cmd tea.Cmd) {
		// Like drive, but keep the escape sequences the model sends.
		queue := []tea.Cmd{cmd}
		for len(queue) > 0 {
			c := queue[0]
			queue = queue[1:]
			if c == nil {
				continue
			}
			done := make(chan tea.Msg, 1)
			go func() { done <- c() }()
			var msg tea.Msg
			select {
			case msg = <-done:
			case <-time.After(300 * time.Millisecond):
				continue
			}
			switch msg := msg.(type) {
			case tea.BatchMsg:
				queue = append(queue, msg...)
				continue
			case tea.RawMsg:
				raws = append(raws, msg.Msg.(string))
				continue
			case nil:
				continue
			}
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
	press := func(k string) {
		r := []rune(k)[0]
		_, cmd := m.Update(tea.KeyPressMsg{Code: r, Text: k})
		run(cmd)
	}
	run(m.Init())
	press("j") // Boards of Canada, "All albums": no cover
	press("3")
	press("j") // a track of the grouped list: still no cover
	if strings.Contains(m.View().Content, placeholderRune) || len(raws) != 0 {
		t.Fatal("cover shown for \"All albums\"")
	}
	press("2")
	press("j") // Geogaddi: now a cover
	if len(raws) == 0 || !strings.Contains(strings.Join(raws, ""), "a=T,U=1") {
		t.Fatal("cover image not sent to the terminal")
	}
	view := m.View().Content
	lines := strings.Split(view, "\n")
	// The cover is at the bottom of the tracks column, above the status bar.
	coverLine := -1
	for i, l := range lines {
		if strings.Contains(l, placeholderRune) {
			coverLine = i
			break
		}
	}
	_, _, w2 := m.columnWidths()
	cols, rows, _ := covers.Box(w2, m.columnHeight())
	if coverLine < 0 || coverLine+rows != m.columnHeight()+1 {
		t.Fatalf("cover starts at line %d, want %d:\n%s", coverLine, m.columnHeight()+1-rows, ansi.Strip(view))
	}
	if n := strings.Count(lines[coverLine], placeholderRune); n != cols {
		t.Fatalf("cover row has %d cells, want %d", n, cols)
	}
	// Right-aligned: in the tracks column (after the last separator), the
	// cover is preceded by w2-cols-1 spaces and followed by one.
	plain := ansi.Strip(lines[coverLine])
	col := []rune(plain[strings.LastIndex(plain, "│")+len("│"):])
	lead := 0
	for lead < len(col) && col[lead] == ' ' {
		lead++
	}
	if lead != w2-cols-1 || col[len(col)-1] != ' ' || !strings.HasPrefix(string(col[lead:]), placeholderRune) {
		t.Fatalf("cover not at the right border: %d leading spaces, want %d", lead, w2-cols-1)
	}
	if !strings.Contains(lines[coverLine], "\x1b[38;5;") {
		t.Fatal("cover row lacks the 256-color image id")
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 120 {
			t.Fatalf("line wider than the terminal (%d)", w)
		}
	}
	if !strings.Contains(ansi.Strip(view), "Track l.a") {
		t.Fatal("track list missing above the cover")
	}

	// An album without artwork shows no cover.
	press("1")
	press("j") // Broadcast
	press("2")
	press("j")
	if strings.Contains(m.View().Content, placeholderRune) {
		t.Fatal("cover shown for an album without artwork")
	}

	// Search: the shown catalog album has a cover too.
	press("s")
	for _, r := range "loscil" {
		press(string(r))
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	run(cmd)
	m.search.results[0].ArtworkURL = srv.URL + "/plume.jpg"
	m.search.albums[0].ArtworkURL = srv.URL + "/plume.jpg"
	press("2")
	press("j")
	press("k")
	if !strings.Contains(m.View().Content, placeholderRune) {
		t.Fatalf("no cover in search:\n%s", ansi.Strip(m.View().Content))
	}
}
