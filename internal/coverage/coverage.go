// Package coverage holds per-test line coverage for a review. margin never runs tests itself: the
// agent runs them and hands the results over, either as Go coverprofiles or as this package's JSON.
package coverage

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redrick/margin/internal/fsx"
)

const SourceGo = "go"

type Test struct {
	Name    string           `json:"name"`
	Package string           `json:"package,omitempty"`
	File    string           `json:"file,omitempty"`
	Source  string           `json:"source,omitempty"`
	Lines   map[string][]int `json:"lines"`
}

// Set is the coverage file. Lines are 1-based. Executable lists, per file, the lines that can run at
// all; without it every non-trivial line counts. Hashes record each file's content when it was
// measured, so the viewer can tell when the code has moved on.
type Set struct {
	Tests      []Test            `json:"tests"`
	Executable map[string][]int  `json:"executable,omitempty"`
	Hashes     map[string]string `json:"hashes,omitempty"`
	Updated    time.Time         `json:"updated"`
}

func Load(path string) (*Set, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func Parse(data []byte) (*Set, error) {
	var s Set
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("coverage: %w", err)
	}
	for i, t := range s.Tests {
		if strings.TrimSpace(t.Name) == "" {
			return nil, fmt.Errorf("coverage: test %d has no name", i+1)
		}
		for file, lines := range t.Lines {
			if slices.ContainsFunc(lines, func(l int) bool { return l < 1 }) {
				return nil, fmt.Errorf("coverage: %s: %s has a line below 1", t.Name, file)
			}
		}
	}
	return &s, nil
}

func (s *Set) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteFileAtomic(path, append(data, '\n'), 0o644)
}

// Files lists every file the set says anything about.
func (s *Set) Files() []string {
	seen := map[string]bool{}
	for _, t := range s.Tests {
		for f := range t.Lines {
			seen[f] = true
		}
	}
	for f := range s.Executable {
		seen[f] = true
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// Merge adds in to s. Tests replace those with the same package and name; with replace set, every
// earlier test from in's source is dropped first, so a fresh Go run forgets deleted tests.
func (s *Set) Merge(in *Set, source string, replace bool) {
	if replace {
		s.Tests = slices.DeleteFunc(s.Tests, func(t Test) bool { return t.Source == source })
	}
	for _, t := range in.Tests {
		if t.Source == "" {
			t.Source = source
		}
		s.Tests = slices.DeleteFunc(s.Tests, func(o Test) bool { return o.Package == t.Package && o.Name == t.Name })
		s.Tests = append(s.Tests, t)
	}
	sort.SliceStable(s.Tests, func(i, j int) bool {
		if s.Tests[i].Package != s.Tests[j].Package {
			return s.Tests[i].Package < s.Tests[j].Package
		}
		return s.Tests[i].Name < s.Tests[j].Name
	})
	for f, lines := range in.Executable {
		if s.Executable == nil {
			s.Executable = map[string][]int{}
		}
		s.Executable[f] = lines
	}
	for f, h := range in.Hashes {
		if s.Hashes == nil {
			s.Hashes = map[string]string{}
		}
		s.Hashes[f] = h
	}
	s.Updated = time.Now().UTC().Truncate(time.Second)
}

// Hash identifies a file's content as margin shows it: sanitised and split into lines.
func Hash(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:8])
}

type Block struct {
	File               string
	StartLine, EndLine int
	Count              int
}

var profileLine = regexp.MustCompile(`^(.+):(\d+)\.\d+,(\d+)\.\d+ \d+ (\d+)$`)

// ParseProfile reads a Go coverprofile.
func ParseProfile(r io.Reader) ([]Block, error) {
	var out []Block
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			first = false
			if !strings.HasPrefix(line, "mode:") {
				return nil, errors.New("not a Go coverprofile: first line is not mode:")
			}
			continue
		}
		if line == "" {
			continue
		}
		m := profileLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("bad coverprofile line %q", line)
		}
		b := Block{File: m[1]}
		b.StartLine, _ = strconv.Atoi(m[2])
		b.EndLine, _ = strconv.Atoi(m[3])
		b.Count, _ = strconv.Atoi(m[4])
		out = append(out, b)
	}
	return out, sc.Err()
}

type Module struct {
	Path string
	Dir  string
}

// Rel maps a coverprofile file name (import path + file) to a repository path.
func Rel(mods []Module, file string) (string, bool) {
	best := -1
	for i, m := range mods {
		if strings.HasPrefix(file, m.Path+"/") && (best < 0 || len(m.Path) > len(mods[best].Path)) {
			best = i
		}
	}
	if best < 0 {
		return "", false
	}
	rest := strings.TrimPrefix(file, mods[best].Path+"/")
	if mods[best].Dir == "" || mods[best].Dir == "." {
		return rest, true
	}
	return mods[best].Dir + "/" + rest, true
}

var moduleLine = regexp.MustCompile(`(?m)^module\s+("?)([^"\s]+)"?\s*$`)

func ModulePath(gomod []byte) string {
	if m := moduleLine.FindSubmatch(gomod); m != nil {
		return string(m[2])
	}
	return ""
}

// FromBlocks turns one test's profile into a Test and the lines that can run, keeping only files in
// keep so the coverage file stays about the review.
func FromBlocks(name, pkg string, blocks []Block, mods []Module, keep map[string]bool) (Test, map[string]map[int]bool) {
	t := Test{Name: name, Package: pkg, Source: SourceGo, Lines: map[string][]int{}}
	exec := map[string]map[int]bool{}
	run := map[string]map[int]bool{}
	for _, b := range blocks {
		file, ok := Rel(mods, b.File)
		if !ok || !keep[file] {
			continue
		}
		if exec[file] == nil {
			exec[file], run[file] = map[int]bool{}, map[int]bool{}
		}
		for l := b.StartLine; l <= b.EndLine; l++ {
			exec[file][l] = true
			if b.Count > 0 {
				run[file][l] = true
			}
		}
	}
	for file, lines := range run {
		t.Lines[file] = SortedLines(lines)
	}
	return t, exec
}

func SortedLines(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
