package tui

import "strings"

// pane is a scrollable, filterable list. It only knows row labels; the model
// maps pane.selected() back to its own data.
type pane struct {
	title  string
	labels []string // one per underlying item, used for filtering
	match  []int    // indices into labels that pass the filter
	cursor int      // index into match
	offset int      // first visible row (index into match)
	filter string
}

func (p *pane) setItems(labels []string) {
	p.labels = labels
	p.refilter()
	p.cursor, p.offset = 0, 0
}

// setItemsKeep replaces the items but keeps the cursor on the item with the
// same underlying index when possible.
func (p *pane) setItemsKeep(labels []string, want int) {
	p.labels = labels
	p.refilter()
	p.selectIndex(want)
}

func (p *pane) refilter() {
	p.match = p.match[:0]
	f := strings.ToLower(p.filter)
	for i, l := range p.labels {
		if f == "" || strings.Contains(strings.ToLower(l), f) {
			p.match = append(p.match, i)
		}
	}
	if p.cursor >= len(p.match) {
		p.cursor = max(0, len(p.match)-1)
	}
}

func (p *pane) setFilter(f string) {
	sel := p.selected()
	p.filter = f
	p.refilter()
	if !p.selectIndex(sel) {
		p.cursor, p.offset = 0, 0
	}
}

// selected is the underlying index under the cursor, or -1.
func (p *pane) selected() int {
	if p.cursor < 0 || p.cursor >= len(p.match) {
		return -1
	}
	return p.match[p.cursor]
}

// selectIndex moves the cursor to underlying index i, if it is visible.
func (p *pane) selectIndex(i int) bool {
	for c, m := range p.match {
		if m == i {
			p.cursor = c
			return true
		}
	}
	return false
}

func (p *pane) move(delta int) {
	if len(p.match) == 0 {
		return
	}
	p.cursor = min(max(p.cursor+delta, 0), len(p.match)-1)
}

func (p *pane) home() { p.cursor = 0 }
func (p *pane) end()  { p.cursor = max(0, len(p.match)-1) }

// scroll keeps the cursor inside a window of height rows.
func (p *pane) scroll(height int) {
	if height <= 0 {
		return
	}
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+height {
		p.offset = p.cursor - height + 1
	}
	p.offset = max(0, min(p.offset, max(0, len(p.match)-height)))
}
