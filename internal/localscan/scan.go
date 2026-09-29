// Package localscan reads the local music folder into the library cache.
// It never writes to the music files.
//
// A scan walks the root folder, compares each file with the stamp (mtime
// and size) of the last scan, and reads only new and changed files, with
// ffprobe: tags, duration, codec, sample rate, bit depth, and whether a
// cover is embedded. Tags that are missing come from the folder names.
package localscan

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/biomassa/scopolamine/internal/library"
)

// audioExts are the file types that count as audio: everything mpv plays.
var audioExts = map[string]bool{
	".flac": true, ".mp3": true, ".m4a": true, ".mp4": true, ".aac": true, ".alac": true,
	".ogg": true, ".oga": true, ".opus": true, ".wav": true, ".aif": true, ".aiff": true,
	".wv": true, ".ape": true, ".dsf": true, ".dff": true, ".mpc": true, ".tta": true,
	".wma": true, ".mka": true,
}

// coverNames are the preferred image names of a folder cover, in order.
var coverNames = []string{"cover", "folder", "front", "album"}

var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true}

// IsAudio reports whether path has an audio file extension.
func IsAudio(path string) bool { return audioExts[strings.ToLower(filepath.Ext(path))] }

// Result counts what a scan did.
type Result struct {
	Files, Changed, Removed int
}

// Scanner scans one library root.
type Scanner struct {
	Root    string
	FFprobe string // program name or path; "" means "ffprobe"
	Workers int    // parallel ffprobe runs; 0 means the CPU count

	probe func(ctx context.Context, path string) (probeResult, error) // tests replace it
}

// entry is a unit of the scan: an audio file, or a cue sheet with the audio
// file that it splits.
type entry struct {
	path  string // the audio file, or the cue sheet
	audio string // the audio file
	cue   *cueSheet
	stamp library.FileStamp
}

// Scan updates store from the files under the root. progress, if not nil,
// gets the number of read files and the number to read.
func (s *Scanner) Scan(ctx context.Context, store *library.Store, progress func(done, total int)) (Result, error) {
	root, err := filepath.Abs(s.Root)
	if err != nil {
		return Result{}, err
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return Result{}, fmt.Errorf("local library root %s: not a folder", root)
	}
	entries, err := collect(root)
	if err != nil {
		return Result{}, err
	}
	old, err := store.LocalFiles(ctx)
	if err != nil {
		return Result{}, err
	}
	var todo []entry
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		seen[e.path] = true
		if st, ok := old[e.path]; !ok || st != e.stamp {
			todo = append(todo, e)
		}
	}
	var removed []string
	for p := range old {
		if !seen[p] {
			removed = append(removed, p)
		}
	}
	res := Result{Files: len(entries), Changed: len(todo), Removed: len(removed)}
	if len(todo) > 0 && s.probe == nil {
		if _, err := findProgram(s.ffprobe()); err != nil {
			return res, err
		}
	}

	files, err := s.readAll(ctx, root, todo, progress)
	if err != nil {
		return res, err
	}
	return res, store.UpdateLocal(ctx, files, removed)
}

func (s *Scanner) ffprobe() string {
	if s.FFprobe != "" {
		return s.FFprobe
	}
	return "ffprobe"
}

// ErrNoFFprobe means ffprobe (FFmpeg) is not installed.
var ErrNoFFprobe = errors.New("ffprobe not found: install FFmpeg to read the local library")

// collect walks root and returns the scan entries. A cue sheet with one
// existing audio file and at least two tracks claims that file; the file
// then does not appear on its own.
func collect(root string) ([]entry, error) {
	var audio []string
	var cues []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable folders are skipped, not fatal
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		switch ext := strings.ToLower(filepath.Ext(p)); {
		case audioExts[ext]:
			audio = append(audio, p)
		case ext == ".cue":
			cues = append(cues, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	claimed := map[string]*cueSheet{}
	claimer := map[string]string{}
	for _, c := range cues {
		sheet, err := readCue(c)
		if err != nil {
			continue
		}
		if f := sheet.singleFile(filepath.Dir(c)); f != "" && claimer[f] == "" {
			claimed[f], claimer[f] = sheet, c
		}
	}
	var out []entry
	for _, a := range audio {
		st, err := stamp(a)
		if err != nil {
			continue
		}
		if cue := claimed[a]; cue != nil {
			cs, err := stamp(claimer[a])
			if err != nil {
				continue
			}
			// Either file changing means a new scan of the pair.
			out = append(out, entry{path: claimer[a], audio: a, cue: cue,
				stamp: library.FileStamp{Mtime: max(st.Mtime, cs.Mtime), Size: st.Size + cs.Size}})
			continue
		}
		out = append(out, entry{path: a, audio: a, stamp: st})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

func stamp(path string) (library.FileStamp, error) {
	st, err := os.Stat(path)
	if err != nil {
		return library.FileStamp{}, err
	}
	return library.FileStamp{Mtime: st.ModTime().UnixNano(), Size: st.Size()}, nil
}

// readAll probes the entries in parallel and builds their library data.
func (s *Scanner) readAll(ctx context.Context, root string, todo []entry, progress func(done, total int)) ([]library.LocalFile, error) {
	workers := s.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	probe := s.probe
	if probe == nil {
		probe = func(ctx context.Context, path string) (probeResult, error) {
			pr, err := runFFprobe(ctx, s.ffprobe(), path)
			if err == nil && pr.codec == "mp3" {
				var audioBytes int64
				pr.vbr, audioBytes = mp3VBR(path)
				if pr.vbr {
					// The average of the audio: from the byte count of the
					// VBR header, else the file's average.
					pr.kbps = pr.avgKbps
					if audioBytes > 0 && pr.duration > 0 {
						pr.kbps = int((float64(audioBytes)*8/pr.duration.Seconds() + 500) / 1000)
					}
				}
			}
			return pr, err
		}
	}
	out := make([]library.LocalFile, len(todo))
	ok := make([]bool, len(todo))
	var (
		mu   sync.Mutex
		done int
		wg   sync.WaitGroup
	)
	jobs := make(chan int)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				e := todo[i]
				pr, err := probe(ctx, e.audio)
				if err == nil {
					out[i], ok[i] = build(root, e, pr), true
				}
				mu.Lock()
				done++
				if progress != nil {
					progress(done, len(todo))
				}
				mu.Unlock()
			}
		}()
	}
	for i := range todo {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	// Files that ffprobe cannot read are left out; the next scan tries them
	// again, because their stamp is not recorded.
	var files []library.LocalFile
	for i := range out {
		if ok[i] {
			files = append(files, out[i])
		}
	}
	return files, nil
}

// build turns a probe result into the library data of one entry.
func build(root string, e entry, pr probeResult) library.LocalFile {
	rel, _ := filepath.Rel(root, filepath.Dir(e.audio))
	folder := filepath.ToSlash(rel)
	if folder == "." {
		folder = ""
	}
	t := pr.tags
	albumArtist := first(t, "album_artist", "albumartist", "album artist")
	artist := first(t, "artist")
	if albumArtist == "" {
		albumArtist = artist
	}
	if albumArtist == "" {
		albumArtist = parentName(folder)
	}
	if artist == "" {
		artist = albumArtist
	}
	album := first(t, "album")
	if album == "" {
		album = baseName(folder)
	}
	date := first(t, "date", "year", "originaldate")
	art := ""
	if pr.embeddedCover {
		art = "embedded:" + e.audio
	} else if img := folderImage(filepath.Dir(e.audio)); img != "" {
		art = "file:" + img
	}

	if e.cue != nil {
		return buildCue(e, pr, folder, albumArtist, album, date, art)
	}

	albumID := library.LocalAlbumID(albumArtist, album, folder)
	num, _ := numberPair(first(t, "track", "tracknumber"))
	disc, _ := numberPair(first(t, "disc", "discnumber"))
	title := first(t, "title")
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(e.audio), filepath.Ext(e.audio))
	}
	return library.LocalFile{
		Path:  e.path,
		Stamp: e.stamp,
		Albums: []library.Album{{
			ID: albumID, Source: library.SourceLocal, Title: album, Artist: albumArtist,
			ReleaseDate: date, Year: library.YearOf(date), Genre: first(t, "genre"), ArtworkURL: art,
		}},
		Tracks: []library.Track{{
			ID: library.LocalTrackID(e.audio, 0), AlbumID: albumID, Title: title, Artist: artist,
			Disc: disc, Number: num, Duration: pr.duration, Playable: true,
			Path: e.audio, Folder: folder, Codec: pr.codec, SampleRate: pr.sampleRate, Bits: pr.bits, Kbps: pr.kbps, VBR: pr.vbr,
		}},
	}
}

// folderImage returns the cover image of a folder: a preferred name
// (cover, folder, front, album), or the only image in the folder.
func folderImage(dir string) string {
	des, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var images []string
	for _, d := range des {
		if !d.IsDir() && imageExts[strings.ToLower(filepath.Ext(d.Name()))] {
			images = append(images, d.Name())
		}
	}
	for _, want := range coverNames {
		for _, n := range images {
			if strings.EqualFold(strings.TrimSuffix(n, filepath.Ext(n)), want) {
				return filepath.Join(dir, n)
			}
		}
	}
	if len(images) == 1 {
		return filepath.Join(dir, images[0])
	}
	return ""
}

// parentName is the folder above the album folder, or the album folder
// itself when it is at the top.
func parentName(folder string) string {
	if i := strings.LastIndexByte(folder, '/'); i >= 0 {
		return baseName(folder[:i])
	}
	return folder
}

func baseName(folder string) string {
	if i := strings.LastIndexByte(folder, '/'); i >= 0 {
		return folder[i+1:]
	}
	return folder
}
