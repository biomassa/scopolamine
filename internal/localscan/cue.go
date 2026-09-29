package localscan

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/biomassa/scopolamine/internal/library"
)

type cueTrack struct {
	num       int
	title     string
	performer string
	start     time.Duration // INDEX 01
}

type cueFile struct {
	name   string
	tracks []cueTrack
}

type cueSheet struct {
	title, performer, date string
	files                  []cueFile
}

// readCue reads a cue sheet. The text is UTF-8 when it is valid UTF-8 (with
// or without a byte order mark), else Windows-1252.
func readCue(path string) (*cueSheet, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the user's music folder
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		if data, err = charmap.Windows1252.NewDecoder().Bytes(data); err != nil {
			return nil, err
		}
	}
	return parseCue(string(data)), nil
}

func parseCue(text string) *cueSheet {
	c := &cueSheet{}
	var file *cueFile
	var track *cueTrack
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		cmd, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)
		switch strings.ToUpper(cmd) {
		case "REM":
			if k, v, ok := strings.Cut(rest, " "); ok && strings.EqualFold(k, "DATE") {
				c.date = unquote(v)
			}
		case "FILE":
			name := rest
			if i := strings.LastIndexByte(rest, ' '); i > 0 && !strings.HasSuffix(rest, `"`) {
				name = rest[:i] // FILE name TYPE
			} else if i := strings.LastIndexByte(rest, '"'); i > 0 {
				name = rest[:i+1]
			}
			c.files = append(c.files, cueFile{name: unquote(name)})
			file, track = &c.files[len(c.files)-1], nil
		case "TRACK":
			if file == nil {
				continue
			}
			numStr, _, _ := strings.Cut(rest, " ")
			n, _ := strconv.Atoi(numStr)
			file.tracks = append(file.tracks, cueTrack{num: n})
			track = &file.tracks[len(file.tracks)-1]
		case "TITLE":
			if track != nil {
				track.title = unquote(rest)
			} else {
				c.title = unquote(rest)
			}
		case "PERFORMER":
			if track != nil {
				track.performer = unquote(rest)
			} else {
				c.performer = unquote(rest)
			}
		case "INDEX":
			idx, t, _ := strings.Cut(rest, " ")
			if track != nil && idx == "01" {
				track.start = cueTime(strings.TrimSpace(t))
			}
		}
	}
	return c
}

// cueTime parses mm:ss:ff (75 frames per second).
func cueTime(s string) time.Duration {
	p := strings.Split(s, ":")
	if len(p) != 3 {
		return 0
	}
	m, _ := strconv.Atoi(p[0])
	sec, _ := strconv.Atoi(p[1])
	f, _ := strconv.Atoi(p[2])
	return time.Duration(m)*time.Minute + time.Duration(sec)*time.Second + time.Duration(f)*time.Second/75
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// singleFile returns the audio file that the sheet splits: the sheet has
// one FILE with at least two tracks, and the file exists in dir. A FILE
// name with another extension (album.wav for album.flac) matches by its
// base name. It returns "" for other sheets, for example the per-track
// sheets that rippers write next to separate track files.
func (c *cueSheet) singleFile(dir string) string {
	if len(c.files) != 1 || len(c.files[0].tracks) < 2 {
		return ""
	}
	want := c.files[0].name
	des, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	base := strings.TrimSuffix(want, filepath.Ext(want))
	var byBase string
	for _, d := range des {
		n := d.Name()
		if d.IsDir() || !IsAudio(n) {
			continue
		}
		if strings.EqualFold(n, want) {
			return filepath.Join(dir, n)
		}
		if byBase == "" && strings.EqualFold(strings.TrimSuffix(n, filepath.Ext(n)), base) {
			byBase = filepath.Join(dir, n)
		}
	}
	return byBase
}

// buildCue makes the tracks of a cue-split file: one per cue track, with
// its start and length in the file.
func buildCue(e entry, pr probeResult, folder, albumArtist, album, date, art string) library.LocalFile {
	c := e.cue
	if c.performer != "" {
		albumArtist = c.performer
	}
	if c.title != "" {
		album = c.title
	}
	if c.date != "" {
		date = c.date
	}
	albumID := library.LocalAlbumID(albumArtist, album, folder)
	f := library.LocalFile{
		Path:  e.path,
		Stamp: e.stamp,
		Albums: []library.Album{{
			ID: albumID, Source: library.SourceLocal, Title: album, Artist: albumArtist,
			ReleaseDate: date, Year: library.YearOf(date), Genre: first(pr.tags, "genre"), ArtworkURL: art,
		}},
	}
	tracks := c.files[0].tracks
	for i, ct := range tracks {
		end := pr.duration
		if i+1 < len(tracks) {
			end = tracks[i+1].start
		}
		title := ct.title
		if title == "" {
			title = "Track " + strconv.Itoa(i+1)
		}
		artist := ct.performer
		if artist == "" {
			artist = albumArtist
		}
		f.Tracks = append(f.Tracks, library.Track{
			ID: library.LocalTrackID(e.audio, i+1), AlbumID: albumID, Title: title, Artist: artist,
			Number: i + 1, Duration: max(0, end-ct.start), Playable: true,
			Path: e.audio, Folder: folder, CueTrack: i + 1, CuePath: e.path, Start: ct.start,
			Codec: pr.codec, SampleRate: pr.sampleRate, Bits: pr.bits, Kbps: pr.kbps, VBR: pr.vbr,
		})
	}
	return f
}
