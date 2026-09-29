// Package mpv plays local files with mpv, which it controls over mpv's JSON
// IPC socket.
//
// mpv decodes with FFmpeg and plays gapless (--gapless-audio, with the next
// file prefetched), with album ReplayGain that falls back to track gain. It
// runs with --no-config and without scripts, so that the playback is always
// the same. A cue-split album plays as one file: mpv loads the cue sheet and
// the cue tracks are its chapters, so the album stays gapless.
package mpv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/biomassa/scopolamine/internal/library"
	"github.com/biomassa/scopolamine/internal/player"
)

// Item is one track to play.
type Item struct {
	ID         string
	Path       string        // the audio file
	CuePath    string        // for a cue track: the cue sheet; "" otherwise
	Start      time.Duration // for a cue track: its start in the file
	Duration   time.Duration
	Title      string
	Artist     string
	Album      string
	ArtworkURL string
	Codec      string
	SampleRate int
	Bits       int
}

// debugLog, when set (tests), receives the IPC traffic.
var debugLog func(string)

// ErrNoMPV means mpv is not installed.
var ErrNoMPV = errors.New("mpv not found: install mpv to play local files")

// Options configures New.
type Options struct {
	Binary  string                        // "" means "mpv"
	Resolve func(id string) (Item, error) // maps PlayTracks ids to items
	Extra   []string                      // more mpv options (tests: --ao=null)
}

// Player is an mpv process.
type Player struct {
	resolve func(id string) (Item, error)
	cmd     *exec.Cmd
	conn    net.Conn
	sock    string

	wmu sync.Mutex // serializes writes to conn

	mu      sync.Mutex
	queue   []Item
	entries []entry // the mpv playlist: one entry per file or cue sheet
	pos     int     // mpv playlist-pos
	timePos time.Duration
	paused  bool
	idle    bool
	volume  float64
	loading bool
	// fileReady is false from a playlist change until mpv reports the new
	// file loaded. Navigation in that time waits in pending: mpv handles
	// playlist and seek commands badly before the file plays.
	fileReady bool
	pending   []func()
	state     player.State

	bcast     player.Broadcast
	closeOnce sync.Once
	done      chan struct{}
}

// entry is one mpv playlist entry and the queue items that it plays.
type entry struct {
	path  string // file or cue sheet
	items []int  // queue indices, in order
}

var _ player.Player = (*Player)(nil)

// New starts mpv. It returns ErrNoMPV when mpv is not installed.
func New(ctx context.Context, o Options) (*Player, error) {
	bin := o.Binary
	if bin == "" {
		bin = "mpv"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, ErrNoMPV
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	sock := filepath.Join(dir, fmt.Sprintf("scopolamine-mpv-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
	args := append([]string{
		"--idle=yes", "--no-video", "--no-terminal", "--no-config", "--load-scripts=no",
		"--gapless-audio=yes", "--prefetch-playlist=yes", "--replaygain=album",
		"--audio-display=no", "--force-window=no", "--keep-open=no",
		"--input-ipc-server=" + sock,
	}, o.Extra...)
	// Stdin, stdout, and stderr stay nil (/dev/null): mpv must never see the
	// terminal of the TUI (see cdp/driverio.go for what goes wrong).
	cmd := exec.Command(path, args...) //nolint:gosec // fixed program and options
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mpv: %w", err)
	}
	p := &Player{resolve: o.Resolve, cmd: cmd, sock: sock, volume: 1, pos: -1, idle: true, done: make(chan struct{})}
	p.state = player.State{Ready: true, QueueIndex: -1, Volume: 1, Local: true}
	go func() { _ = cmd.Wait(); p.shutdown() }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			p.conn = conn
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("mpv: IPC socket did not open: %w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	go p.read()
	for i, prop := range []string{"playlist-pos", "time-pos", "pause", "idle-active", "volume", "seeking"} {
		p.send("observe_property", i+1, prop)
	}
	return p, nil
}

func (p *Player) send(args ...any) {
	b, err := json.Marshal(map[string]any{"command": args})
	if err != nil {
		return
	}
	if debugLog != nil {
		debugLog(time.Now().Format("05.000") + " > " + string(b))
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if p.conn != nil {
		_, _ = p.conn.Write(append(b, '\n'))
	}
}

type event struct {
	Event string          `json:"event"`
	Name  string          `json:"name"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
	File  string          `json:"file_error"`
}

func (p *Player) read() {
	sc := bufio.NewScanner(p.conn)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		if debugLog != nil {
			debugLog(time.Now().Format("05.000") + " < " + sc.Text())
		}
		var ev event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Event {
		case "property-change":
			p.onProperty(ev.Name, ev.Data)
		case "start-file":
			// A new file: the old time position belongs to the old file.
			p.mu.Lock()
			p.timePos, p.fileReady = 0, false
			p.mu.Unlock()
		case "file-loaded":
			p.mu.Lock()
			p.fileReady = true
			run := p.pending
			p.pending = nil
			s := p.snapshot()
			p.mu.Unlock()
			p.bcast.Send(s)
			for _, f := range run {
				f()
			}
		case "end-file":
			if ev.File != "" {
				p.notice(player.State{Error: "mpv: " + ev.File})
			}
		}
	}
	p.shutdown()
}

func (p *Player) onProperty(name string, data json.RawMessage) {
	p.mu.Lock()
	switch name {
	case "playlist-pos":
		// Also changes when entries are inserted before the current one;
		// start-file, not this, marks a new file.
		var v int
		if json.Unmarshal(data, &v) == nil {
			p.pos = v
		}
	case "time-pos":
		var v float64
		if json.Unmarshal(data, &v) == nil {
			p.timePos = time.Duration(v * float64(time.Second))
		}
	case "pause":
		_ = json.Unmarshal(data, &p.paused)
	case "idle-active":
		_ = json.Unmarshal(data, &p.idle)
	case "seeking":
		_ = json.Unmarshal(data, &p.loading)
	case "volume":
		var v float64
		if json.Unmarshal(data, &v) == nil {
			p.volume = v / 100
		}
	}
	s := p.snapshot()
	p.mu.Unlock()
	p.bcast.Send(s)
}

// current returns the queue index of the item that plays, from the playlist
// position and, inside a cue sheet, the time position. mu must be held.
func (p *Player) current() int {
	if p.idle || p.pos < 0 || p.pos >= len(p.entries) {
		return -1
	}
	e := p.entries[p.pos]
	cur := e.items[0]
	for _, i := range e.items {
		if p.queue[i].CuePath != "" && p.timePos+50*time.Millisecond >= p.queue[i].Start {
			cur = i
		}
	}
	return cur
}

// snapshot builds the State. mu must be held.
func (p *Player) snapshot() player.State {
	s := p.state
	s.Volume = p.volume
	s.Loading = p.loading || !p.fileReady
	s.QueueLength = len(p.queue)
	s.QueueIndex = p.current()
	s.Error, s.Log, s.SkippedID, s.NeedsAuth = "", "", "", false
	if s.QueueIndex < 0 {
		s.Playing, s.Track, s.Position, s.Format = false, nil, 0, ""
		return s
	}
	it := p.queue[s.QueueIndex]
	s.Playing = !p.paused
	s.Position = max(0, p.timePos-it.Start)
	s.Track = &player.NowPlaying{ID: it.ID, Title: it.Title, Artist: it.Artist, Album: it.Album,
		ArtworkURL: it.ArtworkURL, Duration: it.Duration}
	s.Format = FormatLabel(it.Codec, it.SampleRate, it.Bits)
	return s
}

// FormatLabel is the status-bar text for a file format; see
// library.FormatLabel.
func FormatLabel(codec string, rate, bits int) string { return library.FormatLabel(codec, rate, bits) }

func (p *Player) notice(n player.State) {
	p.mu.Lock()
	s := p.snapshot()
	p.mu.Unlock()
	s.Error, s.Log = n.Error, n.Log
	p.bcast.Send(s)
}

// PlayTracks replaces the queue with ids and plays from ids[start].
// Consecutive tracks of one cue sheet become one playlist entry.
func (p *Player) PlayTracks(ids []string, start int) error {
	if p.resolve == nil {
		return errors.New("mpv: no resolver")
	}
	var queue []Item
	startIdx := 0
	for i, id := range ids {
		it, err := p.resolve(id)
		if err != nil {
			continue
		}
		if i == start {
			startIdx = len(queue)
		}
		queue = append(queue, it)
	}
	if len(queue) == 0 {
		return errors.New("nothing playable")
	}
	var entries []entry
	for i, it := range queue {
		if n := len(entries); n > 0 && it.CuePath != "" && entries[n-1].path == it.CuePath {
			entries[n-1].items = append(entries[n-1].items, i)
			continue
		}
		path := it.Path
		if it.CuePath != "" {
			path = it.CuePath
		}
		entries = append(entries, entry{path: path, items: []int{i}})
	}
	startEntry := 0
	for ei, e := range entries {
		for _, i := range e.items {
			if i == startIdx {
				startEntry = ei
			}
		}
	}
	p.mu.Lock()
	p.queue, p.entries, p.pos, p.timePos, p.idle = queue, entries, -1, 0, false
	p.fileReady, p.pending = false, nil
	p.mu.Unlock()

	// The start entry replaces the playlist, which starts exactly that file
	// (at its cue track, by time: mpv's chapter numbers are unreliable at
	// chapter boundaries). The entries after it are appended.
	opts := "pause=no"
	if it := queue[startIdx]; it.CuePath != "" && it.Start > 0 {
		opts += ",start=" + strconv.FormatFloat(it.Start.Seconds()+0.001, 'f', 3, 64)
	}
	p.send("loadfile", entries[startEntry].path, "replace", -1, opts)
	for _, e := range entries[startEntry+1:] {
		p.send("loadfile", e.path, "append")
	}
	// Insert the earlier entries only after the start file plays: an insert
	// before a pending start shifts the positions, and mpv then starts the
	// inserted file instead.
	before := entries[:startEntry]
	p.whenLoaded(func() {
		for i, e := range before {
			p.send("loadfile", e.path, "insert-at", i)
		}
	})
	return nil
}

// whenLoaded runs f now, or after the current file has loaded.
func (p *Player) whenLoaded(f func()) {
	p.mu.Lock()
	if !p.fileReady {
		p.pending = append(p.pending, f)
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	f()
}

// seekTo seeks to an absolute time in the current file.
func (p *Player) seekTo(secs float64) {
	p.whenLoaded(func() { p.send("seek", secs, "absolute") })
}

func (p *Player) Play() error   { p.send("set_property", "pause", false); return nil }
func (p *Player) Pause() error  { p.send("set_property", "pause", true); return nil }
func (p *Player) Toggle() error { p.send("cycle", "pause"); return nil }

// Stop ends the playback and clears the queue.
func (p *Player) Stop() error {
	p.send("stop")
	p.mu.Lock()
	p.queue, p.entries, p.pos = nil, nil, -1
	p.mu.Unlock()
	return nil
}

// Next goes to the next track: the next chapter inside a cue sheet, else
// the next playlist entry.
func (p *Player) Next() error {
	p.whenLoaded(p.next)
	return nil
}

func (p *Player) next() {
	p.mu.Lock()
	cur := p.current()
	var next *Item
	sameEntry := false
	if cur >= 0 && cur+1 < len(p.queue) {
		n := p.queue[cur+1]
		next = &n
		sameEntry = n.CuePath != "" && n.CuePath == p.queue[cur].CuePath
	}
	p.mu.Unlock()
	switch {
	case next == nil:
	case sameEntry:
		p.seekTo(next.Start.Seconds() + 0.001)
	default:
		p.mu.Lock()
		p.fileReady = false
		p.mu.Unlock()
		p.send("playlist-next")
	}
}

// Previous restarts the track, or goes to the previous track near its start.
func (p *Player) Previous() error {
	p.whenLoaded(p.previous)
	return nil
}

func (p *Player) previous() {
	p.mu.Lock()
	cur := p.current()
	var it, prev Item
	havePrev, sameEntry := false, false
	pos := time.Duration(0)
	if cur >= 0 {
		it = p.queue[cur]
		pos = p.timePos - it.Start
		if cur > 0 {
			prev, havePrev = p.queue[cur-1], true
			sameEntry = prev.CuePath != "" && prev.CuePath == it.CuePath
		}
	}
	p.mu.Unlock()
	switch {
	case cur < 0:
	case pos > 3*time.Second || !havePrev:
		p.seekTo(it.Start.Seconds())
	case sameEntry:
		p.seekTo(prev.Start.Seconds() + 0.001)
	default:
		p.mu.Lock()
		p.fileReady = false // the seek below waits for the previous file
		p.mu.Unlock()
		p.send("playlist-prev")
		if prev.CuePath != "" { // the last cue track of the previous sheet
			p.seekTo(prev.Start.Seconds() + 0.001)
		}
	}
}

// Seek goes to a position in the current track.
func (p *Player) Seek(pos time.Duration) error {
	p.whenLoaded(func() { p.seek(pos) })
	return nil
}

func (p *Player) seek(pos time.Duration) {
	p.mu.Lock()
	cur := p.current()
	var start time.Duration
	if cur >= 0 {
		start = p.queue[cur].Start
	}
	p.mu.Unlock()
	if cur >= 0 {
		p.send("seek", (start + max(0, pos)).Seconds(), "absolute")
	}
}

// SetVolume sets mpv's volume (0..1 → 0..100).
func (p *Player) SetVolume(v float64) error {
	p.send("set_property", "volume", min(1, max(0, v))*100)
	return nil
}

func (p *Player) State() player.State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshot()
}

func (p *Player) Subscribe() <-chan player.State { return p.bcast.Subscribe() }

func (p *Player) shutdown() {
	p.closeOnce.Do(func() {
		close(p.done)
		p.wmu.Lock()
		if p.conn != nil {
			_ = p.conn.Close()
		}
		p.wmu.Unlock()
		p.bcast.Close()
		_ = os.Remove(p.sock)
	})
}

// Close quits mpv.
func (p *Player) Close() error {
	p.send("quit")
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
	}
	p.shutdown()
	return nil
}
