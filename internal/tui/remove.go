package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/biomassa/scopolamine/internal/library"
)

// confirmation is a pending y/n question shown in the status line.
type confirmation struct {
	prompt string
	yes    func() tea.Cmd
}

type removedMsg struct {
	album library.Album // library id in ID
	err   error
}

// confirmDelete asks before removing album (library id in ID) from the
// user's Apple Music library.
func (m *Model) confirmDelete(a library.Album) tea.Cmd {
	if m.deps.Catalog == nil {
		return m.flash("removing needs Apple Music (not available offline)", true)
	}
	if m.removing[a.ID] {
		return nil
	}
	m.confirm = &confirmation{
		prompt: "Remove “" + a.Title + "” by " + a.Artist + " from your Apple Music library? y/n",
		yes:    func() tea.Cmd { return m.deleteAlbum(a) },
	}
	return nil
}

func (m *Model) deleteAlbum(a library.Album) tea.Cmd {
	if m.removing == nil {
		m.removing = map[string]bool{}
	}
	m.removing[a.ID] = true
	cat, store := m.deps.Catalog, m.deps.Store
	return tea.Batch(m.flash("removing “"+a.Title+"”…", false), func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, time.Minute)
		defer cancel()
		if err := cat.DeleteLibraryAlbum(ctx, a.ID); err != nil {
			return removedMsg{album: a, err: err}
		}
		return removedMsg{album: a, err: store.DeleteAlbum(ctx, a.ID)}
	})
}

func (m *Model) onRemoved(msg removedMsg) tea.Cmd {
	delete(m.removing, msg.album.ID)
	if msg.err != nil {
		return m.flash("could not remove “"+msg.album.Title+"”: "+msg.err.Error(), true)
	}
	if m.search != nil {
		_ = m.updateSearch(msg)
	}
	return tea.Batch(m.refreshLibrary(), m.flash("removed “"+msg.album.Title+"” from your library", false))
}

// refreshLibrary reloads artists and forces the albums and tracks of the
// current selection to be re-read from the store.
func (m *Model) refreshLibrary() tea.Cmd {
	m.albumsFor = ""
	m.tracksFor = ""
	return m.loadArtists()
}

// libraryDeleteTarget is the album D acts on in the library view: the
// selected album, or in Tracks the album of the selected row.
func (m *Model) libraryDeleteTarget() (library.Album, bool) {
	if m.focus == paneTracks {
		if i := m.panes[paneTracks].selected(); i >= 0 && i < len(m.trackRows) {
			if r := m.trackRows[i]; r.kind != rowAll && r.album.ID != "" {
				return r.album, true
			}
		}
		if a, ok := m.selectedAlbum(); ok {
			return a, true
		}
		return library.Album{}, false
	}
	if m.focus == paneAlbums {
		return m.selectedAlbum()
	}
	return library.Album{}, false
}
