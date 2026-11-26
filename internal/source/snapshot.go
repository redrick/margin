package source

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/redrick/margin/internal/gitx"
	"github.com/redrick/margin/internal/review"
)

// Snapshot tracks the inputs of one built comparison so the GUI can reuse either layout.
type Snapshot struct {
	review                *review.Review
	baseSHA, head         string
	inputs, current, base fileStamps
	changes               []gitx.Change
	changesErr            error
	listed, invalid       bool
}

// NewSnapshot records the review and its sidecars before they are read.
func NewSnapshot(paths ...string) *Snapshot {
	s := &Snapshot{inputs: fileStamps{}, current: fileStamps{}, base: fileStamps{}}
	for _, path := range paths {
		s.track(s.inputs, os.Stat, path)
	}
	return s
}

// TrackSnapshot records the file reads and change list used by this comparison.
func (f *Files) TrackSnapshot(s *Snapshot, rv *review.Review) {
	f.snapshot = s
	s.review = rv
	s.baseSHA, s.head = f.baseSHA, f.head
	if f.staged && f.head == "" {
		out, err := gitx.Run(f.Repo, "rev-parse", "--path-format=absolute", "--git-path", "index")
		index := strings.TrimSuffix(string(out), "\n")
		if err != nil || !filepath.IsAbs(index) {
			s.invalid = true
		} else {
			s.track(s.inputs, os.Stat, index)
		}
	}
}

// Matches checks live inputs without re-reading Git blobs or rebuilding either document.
func (s *Snapshot) Matches() bool {
	if s.invalid || s.changesErr != nil || !s.inputs.match(os.Stat) {
		return false
	}
	rv := s.review
	if s.baseSHA != "" && s.head != "" && rv.Base == s.baseSHA && rv.Head == s.head {
		return true
	}
	f, err := Open(rv)
	if err != nil {
		return false
	}
	defer f.Close()
	if f.baseSHA != s.baseSHA || f.head != s.head || !s.current.match(f.root.Stat) {
		return false
	}
	if base, ok := f.base.(dirReader); ok && !s.base.match(base.root.Stat) {
		return false
	}
	if f.baseSHA != "" && f.head != "" {
		return true
	}
	changes, err := f.Changed()
	return err == nil && slices.Equal(changes, s.changes)
}

// AppendWatchPaths includes inputs read by either layout, including missing files.
func (s *Snapshot) AppendWatchPaths(paths []string) []string {
	for path := range s.inputs {
		paths = append(paths, path)
	}
	for path := range s.current {
		paths = append(paths, filepath.Join(s.review.RepoDir(), path))
	}
	for path := range s.base {
		paths = append(paths, filepath.Join(s.review.BaseDirPath(), path))
	}
	return paths
}

// A nil stamp means the file was absent.
type fileStamps map[string]os.FileInfo

func (s *Snapshot) track(files fileStamps, stat func(string) (os.FileInfo, error), path string) {
	if _, tracked := files[path]; tracked {
		return
	}
	info, err := stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.invalid = true
		return
	}
	files[path] = info
}

func (files fileStamps) match(stat func(string) (os.FileInfo, error)) bool {
	for path, before := range files {
		after, err := stat(path)
		if before == nil && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || before == nil || !os.SameFile(before, after) ||
			before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
			return false
		}
	}
	return true
}
