package watch

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	w        *fsnotify.Watcher
	onChange func()

	mu    sync.Mutex
	dirs  map[string]bool
	files map[string]bool
	timer *time.Timer
}

func New(onChange func()) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{w: fw, onChange: onChange, dirs: map[string]bool{}, files: map[string]bool{}}
	go w.loop()
	return w, nil
}

// Set replaces the watched files. Directories are watched so atomic renames are seen.
func (w *Watcher) Set(paths []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	files := map[string]bool{}
	dirs := map[string]bool{}
	for _, p := range paths {
		p = filepath.Clean(p)
		files[p] = true
		dirs[filepath.Dir(p)] = true
	}
	for d := range w.dirs {
		if !dirs[d] {
			w.w.Remove(d)
		}
	}
	for d := range dirs {
		if !w.dirs[d] {
			if err := w.w.Add(d); err != nil {
				delete(dirs, d)
			}
		}
	}
	w.files, w.dirs = files, dirs
}

func (w *Watcher) Close() { w.w.Close() }

func (w *Watcher) loop() {
	for {
		select {
		case ev, ok := <-w.w.Events:
			if !ok {
				return
			}
			w.mu.Lock()
			if w.files[filepath.Clean(ev.Name)] {
				if w.timer != nil {
					w.timer.Stop()
				}
				w.timer = time.AfterFunc(200*time.Millisecond, w.onChange)
			}
			w.mu.Unlock()
		case _, ok := <-w.w.Errors:
			if !ok {
				return
			}
		}
	}
}
