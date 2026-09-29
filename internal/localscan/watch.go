package localscan

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchQuiet is how long the folder must be quiet after a change before
// onChange runs, so that a download or a large copy starts one scan, not
// one per file.
const watchQuiet = 3 * time.Second

// Watch calls onChange when files under root change, after the changes
// have stopped for watchQuiet. It watches every folder under root, and new
// folders when they appear. It returns when ctx ends.
func Watch(ctx context.Context, root string, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()
	addTree := func(dir string) {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") && p != root {
				return filepath.SkipDir
			}
			_ = w.Add(p)
			return nil
		})
	}
	addTree(root)

	var timer *time.Timer
	fire := make(chan struct{}, 1)
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Has(fsnotify.Create) {
				addTree(ev.Name) // a new folder (a no-op for a file)
			}
			if timer == nil {
				timer = time.AfterFunc(watchQuiet, func() {
					select {
					case fire <- struct{}{}:
					default:
					}
				})
			} else {
				timer.Reset(watchQuiet)
			}
		case <-fire:
			onChange()
		case <-w.Errors:
			// An overflow or a removed watch: a later event still triggers
			// a scan, and the scan itself finds every change.
		}
	}
}
