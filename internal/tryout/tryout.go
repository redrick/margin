// Package tryout changes lines of the working tree for one test run and puts them back after, so a
// reader can check whether the tests that run a line notice when it is wrong.
//
// Before a file is written, what it held is saved under the state folder; a margin that died during
// a run puts those files back the next time it starts (Recover).
package tryout

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/target"
	"github.com/redrick/margin/internal/text"
)

// Edit replaces the lines Old holds, from Line on, as the review shows them, with New. An empty New
// deletes them.
type Edit struct {
	File string `json:"file"`
	Line int    `json:"line"` // 1-based, the first line Old holds
	Old  string `json:"old"`
	New  string `json:"new"`
}

func (e Edit) Lines() []string {
	if e.New == "" {
		return nil
	}
	return split(e.New)
}

// OldLines are the lines the edit replaces.
func (e Edit) OldLines() []string { return split(e.Old) }

// End is the last line the edit replaces, 1-based.
func (e Edit) End() int { return e.Line + len(e.OldLines()) - 1 }

func split(s string) []string { return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") }

// Trial is a set of edits written to the working tree, until Restore.
type Trial struct {
	Repo  string
	Edits []Edit

	mu    sync.Mutex
	files []*record
	done  bool
}

// record is what the backup file holds for one changed file.
type record struct {
	Repo     string `json:"repo"`
	File     string `json:"file"`
	PID      int    `json:"pid"`
	Original []byte `json:"original"`
	Wrote    []byte `json:"wrote"`
	// Hunks say where the edits sit in Wrote, bottom first, to undo them by hand when the file
	// changed again during the run.
	Hunks []hunk `json:"hunks"`
}

type hunk struct {
	At  int      `json:"at"` // 0-based, in Wrote
	Old []string `json:"old"`
	New []string `json:"new"`
	// Above is the line over the edit in Wrote, so a deletion is put back where it was.
	Above *string `json:"above,omitempty"`
}

var (
	liveMu sync.Mutex
	live   = map[*Trial]bool{}
)

// Dir is where backups are kept while a trial runs.
func Dir() string { return filepath.Join(target.StateDir(), "tryout") }

// Check reports whether the edits can be made: every line still reads, on disk, what the review shows.
func Check(repo string, edits []Edit) error {
	_, err := plan(repo, edits)
	return err
}

// Apply writes the edits. Restore must follow, whatever the run did.
func Apply(repo string, edits []Edit) (*Trial, error) {
	recs, err := plan(repo, edits)
	if err != nil {
		return nil, err
	}
	t := &Trial{Repo: repo, Edits: edits}
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return nil, err
	}
	for _, rc := range recs {
		data, err := json.Marshal(rc)
		if err == nil {
			err = os.WriteFile(backupPath(rc.Repo, rc.File), data, 0o600)
		}
		if err == nil {
			err = writeFile(repo, rc.File, rc.Wrote)
		}
		if err != nil {
			t.files = append(t.files, rc)
			t.Restore()
			return nil, fmt.Errorf("%s: %w", rc.File, err)
		}
		t.files = append(t.files, rc)
	}
	liveMu.Lock()
	live[t] = true
	liveMu.Unlock()
	return t, nil
}

// Write makes the edits for good, without a backup.
func Write(repo string, edits []Edit) error {
	recs, err := plan(repo, edits)
	if err != nil {
		return err
	}
	for _, rc := range recs {
		if err := writeFile(repo, rc.File, rc.Wrote); err != nil {
			return fmt.Errorf("%s: %w", rc.File, err)
		}
	}
	return nil
}

// Restore puts every file back. A file that changed again since the edit gets only the edited lines
// put back; when even those moved, the edit stays and the error says where.
func (t *Trial) Restore() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return nil
	}
	t.done = true
	liveMu.Lock()
	delete(live, t)
	liveMu.Unlock()
	var errs []error
	for _, rc := range t.files {
		if err := restore(rc); err != nil {
			errs = append(errs, err)
			continue
		}
		os.Remove(backupPath(rc.Repo, rc.File))
	}
	return errors.Join(errs...)
}

// RestoreAll undoes every trial this process still has applied, for when margin quits mid-run.
func RestoreAll() {
	liveMu.Lock()
	ts := make([]*Trial, 0, len(live))
	for t := range live {
		ts = append(ts, t)
	}
	liveMu.Unlock()
	for _, t := range ts {
		t.Restore()
	}
}

// Recover puts back files a margin that is gone left changed, and says which.
func Recover() ([]string, error) {
	entries, err := os.ReadDir(Dir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var fixed []string
	var errs []error
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(Dir(), e.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var rc record
		if json.Unmarshal(data, &rc) != nil {
			os.Remove(p)
			continue
		}
		if rc.PID != os.Getpid() && alive(rc.PID) {
			continue
		}
		if err := restore(&rc); err != nil {
			errs = append(errs, err)
			continue
		}
		os.Remove(p)
		fixed = append(fixed, filepath.Join(rc.Repo, rc.File))
	}
	return fixed, errors.Join(errs...)
}

func plan(repo string, edits []Edit) ([]*record, error) {
	if len(edits) == 0 {
		return nil, errors.New("nothing to change")
	}
	byFile := map[string][]Edit{}
	var order []string
	for _, e := range edits {
		if err := review.CheckPath(e.File); err != nil {
			return nil, err
		}
		if _, ok := byFile[e.File]; !ok {
			order = append(order, e.File)
		}
		byFile[e.File] = append(byFile[e.File], e)
	}
	var recs []*record
	for _, file := range order {
		orig, err := readFile(repo, file)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if _, err := os.Stat(backupPath(repo, file)); err == nil {
			return nil, fmt.Errorf("%s is already being tried; wait for that run to end", file)
		}
		lines := strings.Split(string(orig), "\n")
		es := byFile[file]
		sort.Slice(es, func(a, b int) bool { return es[a].Line > es[b].Line })
		rc := &record{Repo: repo, File: file, PID: os.Getpid(), Original: orig}
		last := len(lines) + 1
		for _, e := range es {
			i, old := e.Line-1, e.OldLines()
			end := i + len(old) - 1
			if i < 0 || end >= len(lines) || (end == len(lines)-1 && lines[end] == "") {
				return nil, fmt.Errorf("%s has no line %d", file, e.End())
			}
			if e.End() >= last {
				return nil, fmt.Errorf("%s:%d is changed twice", file, e.End())
			}
			last = e.Line
			cr := strings.HasSuffix(lines[i], "\r")
			for k, want := range old {
				if got := text.Sanitize(strings.TrimSuffix(lines[i+k], "\r")); got != text.Sanitize(want) {
					return nil, fmt.Errorf("%s:%d on disk is not what the review shows (%q); reload, or check the code out", file, e.Line+k, strings.TrimSpace(got))
				}
			}
			nl := e.Lines()
			if cr {
				for k := range nl {
					nl[k] += "\r"
				}
			}
			rc.Hunks = append(rc.Hunks, hunk{At: i, Old: append([]string{}, lines[i:end+1]...), New: nl})
			lines = append(lines[:i], append(nl, lines[end+1:]...)...)
		}
		// Hunks were made bottom first, so each At still holds in the final file once the ones
		// above it shift it: recompute them top down.
		shift := 0
		for k := len(rc.Hunks) - 1; k >= 0; k-- {
			h := &rc.Hunks[k]
			h.At += shift
			shift += len(h.New) - len(h.Old)
			if h.At > 0 {
				above := lines[h.At-1]
				h.Above = &above
			}
		}
		rc.Wrote = []byte(strings.Join(lines, "\n"))
		recs = append(recs, rc)
	}
	return recs, nil
}

func restore(rc *record) error {
	cur, err := readFile(rc.Repo, rc.File)
	if errors.Is(err, fs.ErrNotExist) {
		return writeFile(rc.Repo, rc.File, rc.Original)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", rc.File, err)
	}
	switch {
	case bytes.Equal(cur, rc.Original):
		return nil
	case bytes.Equal(cur, rc.Wrote):
		return writeFile(rc.Repo, rc.File, rc.Original)
	}
	lines := strings.Split(string(cur), "\n")
	for _, h := range rc.Hunks {
		if h.At+len(h.New) > len(lines) || !equal(lines[h.At:h.At+len(h.New)], h.New) || (len(h.New) == 0 && h.Above != nil && lines[h.At-1] != *h.Above) {
			return fmt.Errorf("%s changed during the test run and margin could not find its edit at line %d to undo it; the file before the run is in %s",
				rc.File, h.At+1, backupPath(rc.Repo, rc.File))
		}
	}
	for _, h := range rc.Hunks {
		lines = append(lines[:h.At], append(append([]string{}, h.Old...), lines[h.At+len(h.New):]...)...)
	}
	return writeFile(rc.Repo, rc.File, []byte(strings.Join(lines, "\n")))
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func backupPath(repo, file string) string {
	sum := sha256.Sum256([]byte(repo + "\x00" + file))
	return filepath.Join(Dir(), hex.EncodeToString(sum[:8])+".json")
}

func readFile(repo, file string) ([]byte, error) {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(filepath.FromSlash(file))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// writeFile overwrites the file in place, so its mode, owner and links stay.
func writeFile(repo, file string, data []byte) error {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := root.OpenFile(filepath.FromSlash(file), os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
