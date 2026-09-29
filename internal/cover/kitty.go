// Package cover shows album covers with the kitty graphics protocol and its
// Unicode placeholders (kitty and Ghostty).
//
// With placeholders, the terminal receives each image once, with an id. The
// screen then contains ordinary text cells: a placeholder character per
// cell, with diacritics for the row and the column of the image, and the
// image id as the foreground color. The terminal draws the image part in
// each such cell. For the TUI renderer the cover is therefore plain text,
// so redraws, scrolling, and resizing need no special handling.
package cover

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// placeholder is the kitty Unicode placeholder character.
const placeholder = '\U0010EEEE'

// MaxCells is the largest cover side, in cells, that the diacritics table
// can address.
var MaxCells = len(diacritics)

// Supported reports whether the terminal can show covers. It checks for
// kitty and Ghostty, outside tmux and screen. SCOPOLAMINE_COVERS=0 turns
// covers off, and SCOPOLAMINE_COVERS=1 turns them on for other terminals.
func Supported() bool {
	switch os.Getenv("SCOPOLAMINE_COVERS") {
	case "0", "off", "false", "no":
		return false
	case "1", "on", "true", "yes":
		return true
	}
	if os.Getenv("TMUX") != "" || strings.HasPrefix(os.Getenv("TERM"), "screen") {
		return false // placeholders need passthrough there
	}
	term := os.Getenv("TERM")
	return term == "xterm-kitty" || os.Getenv("KITTY_WINDOW_ID") != "" ||
		term == "xterm-ghostty" || os.Getenv("TERM_PROGRAM") == "ghostty" ||
		os.Getenv("GHOSTTY_RESOURCES_DIR") != ""
}

// CellSize returns the size of one terminal cell in pixels. When the
// terminal does not report it, it returns a common 10×20.
func CellSize() (w, h int) {
	for _, fd := range []uintptr{os.Stdout.Fd(), os.Stdin.Fd()} {
		ws, err := unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ)
		if err == nil && ws.Col > 0 && ws.Row > 0 && ws.Xpixel > 0 && ws.Ypixel > 0 {
			return int(ws.Xpixel) / int(ws.Col), int(ws.Ypixel) / int(ws.Row)
		}
	}
	return 10, 20
}

// chunk is the largest payload of one graphics command, per the protocol.
const chunk = 4096

// Transmit returns the escape sequences that send a PNG image with the
// given id and make a virtual placement of cols×rows cells for it. The
// terminal fits the image into that area.
func Transmit(id uint32, png []byte, cols, rows int) string {
	data := base64.StdEncoding.EncodeToString(png)
	var b strings.Builder
	first := true
	for len(data) > 0 || first {
		part := data[:min(chunk, len(data))]
		data = data[len(part):]
		more := 0
		if len(data) > 0 {
			more = 1
		}
		if first {
			// a=T: transmit and place; U=1: virtual placement for
			// placeholders; q=2: no replies, which would reach the TUI
			// as input.
			fmt.Fprintf(&b, "\x1b_Ga=T,U=1,f=100,t=d,i=%d,c=%d,r=%d,q=2,m=%d;%s\x1b\\", id, cols, rows, more, part)
			first = false
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d,q=2;%s\x1b\\", more, part)
		}
	}
	return b.String()
}

// Delete returns the escape sequence that removes image id and frees its
// data in the terminal.
func Delete(id uint32) string {
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id)
}

// Placeholders returns rows lines of cols placeholder cells for image id.
// The caller colors each line with ColorFor(id).
func Placeholders(cols, rows int) []string {
	cols, rows = min(cols, MaxCells), min(rows, MaxCells)
	lines := make([]string, rows)
	var b strings.Builder
	for r := range rows {
		b.Reset()
		for c := range cols {
			b.WriteRune(placeholder)
			b.WriteRune(diacritics[r])
			b.WriteRune(diacritics[c])
		}
		lines[r] = b.String()
	}
	return lines
}

// ID range: the id travels as a 256-color foreground index, which every
// color profile keeps. Indices 0–15 are avoided because some renderers
// write them as the basic 30–37/90–97 codes.
const (
	firstID = 16
	lastID  = 255
)
