package review

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	SideCurrent     = "current"
	SideBase        = "base"
	MergeBasePrefix = "merge-base:"
)

var reservedIDs = map[string]bool{"overview": true, "tests": true, "recap": true}

type Review struct {
	Version  int        `yaml:"version"`
	Repo     string     `yaml:"repo"`
	Base     string     `yaml:"base,omitempty"`
	Head     string     `yaml:"head,omitempty"`
	BaseDir  string     `yaml:"base_dir,omitempty"`
	Kicker   string     `yaml:"kicker,omitempty"`
	Title    string     `yaml:"title"`
	Summary  string     `yaml:"summary,omitempty"`
	Flow     string     `yaml:"flow,omitempty"`
	Stations []Station  `yaml:"stations"`
	Skip     []Skip     `yaml:"skip,omitempty"`
	Renames  []Rename   `yaml:"renames,omitempty"`
	Tests    []TestFile `yaml:"tests,omitempty"`

	Path string `yaml:"-"`
}

type Station struct {
	ID      string   `yaml:"id"`
	Title   string   `yaml:"title"`
	Lede    string   `yaml:"lede,omitempty"`
	Risk    string   `yaml:"risk,omitempty"`
	Concern string   `yaml:"concern,omitempty"`
	Tests   []string `yaml:"tests,omitempty"`
	Parts   []Part   `yaml:"parts"`
	Notes   []Note   `yaml:"notes,omitempty"`
}

type Part struct {
	File  string `yaml:"file"`
	Func  string `yaml:"func,omitempty"`
	From  string `yaml:"from,omitempty"`
	To    string `yaml:"to,omitempty"`
	Lines string `yaml:"lines,omitempty"`
	Hunks bool   `yaml:"hunks,omitempty"`
	Side  string `yaml:"side,omitempty"`

	BaseFile string `yaml:"base_file,omitempty"`
}

type Note struct {
	At         string `yaml:"at"`
	Nth        int    `yaml:"nth,omitempty"`
	File       string `yaml:"file,omitempty"`
	Kind       string `yaml:"kind,omitempty"`
	Confidence string `yaml:"confidence,omitempty"`
	Q          string `yaml:"q,omitempty"`
	QID        string `yaml:"qid,omitempty"`
	Text       string `yaml:"text"`
	Evidence   string `yaml:"evidence,omitempty"`
	Changed    string `yaml:"changed,omitempty"`
}

var (
	NoteKinds   = []string{"issue", "question", "ok", "info", "nit"}
	Confidences = []string{"high", "medium", "low"}
	Risks       = []string{"high", "medium", "low"}
)

func oneOf(v string, allowed []string) bool {
	return v == "" || slices.Contains(allowed, v)
}

type Skip struct {
	What string `yaml:"what"`
	Why  string `yaml:"why,omitempty"`
}

type Rename struct {
	Before string `yaml:"before"`
	After  string `yaml:"after"`
}

type TestFile struct {
	File string `yaml:"file"`
	Why  string `yaml:"why,omitempty"`
}

func Load(path string) (*Review, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	return Parse(abs, data)
}

func Parse(path string, data []byte) (*Review, error) {
	var r Review
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: empty review file", path)
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	r.Path = path
	if err := r.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

func (r *Review) validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if r.Version != 1 {
		add("version must be 1, got %d", r.Version)
	}
	if r.Repo == "" {
		add("repo is required")
	}
	if r.Base != "" && r.BaseDir != "" {
		add("use either base or base_dir, not both")
	}
	if strings.HasPrefix(strings.TrimPrefix(r.Base, MergeBasePrefix), "-") {
		add("base %q must not start with '-'", r.Base)
	}
	if strings.HasPrefix(r.Head, "-") {
		add("head %q must not start with '-'", r.Head)
	}
	if r.Head != "" && r.Base == "" {
		add("head needs base")
	}
	seen := map[string]bool{}
	for i, s := range r.Stations {
		where := fmt.Sprintf("stations[%d]", i)
		switch {
		case s.ID == "":
			add("%s: id is required", where)
		case strings.ContainsAny(s.ID, ": \t"):
			add("%s: id %q must not contain ':' or spaces", where, s.ID)
		case reservedIDs[s.ID]:
			add("%s: id %q is reserved", where, s.ID)
		case seen[s.ID]:
			add("%s: duplicate id %q", where, s.ID)
		}
		seen[s.ID] = true
		where = fmt.Sprintf("station %s", s.ID)
		if !oneOf(s.Risk, Risks) {
			add("%s: risk must be one of %s", where, strings.Join(Risks, ", "))
		}
		if len(s.Parts) == 0 {
			add("%s: needs at least one part", where)
		}
		for j, p := range s.Parts {
			pw := fmt.Sprintf("%s parts[%d]", where, j)
			if err := CheckPath(p.File); err != nil {
				add("%s: %v", pw, err)
			}
			n := 0
			for _, v := range []string{p.Func, p.From, p.Lines} {
				if v != "" {
					n++
				}
			}
			if p.Hunks {
				n++
			}
			if n > 1 {
				add("%s: use only one of func, from/to, lines, hunks", pw)
			}
			if p.BaseFile != "" {
				if err := CheckPath(p.BaseFile); err != nil {
					add("%s: base_file: %v", pw, err)
				}
			}
			if p.To != "" && p.From == "" {
				add("%s: to needs from", pw)
			}
			if p.Side != "" && p.Side != SideCurrent && p.Side != SideBase {
				add("%s: side must be %q or %q", pw, SideCurrent, SideBase)
			}
		}
		for j, n := range s.Notes {
			nw := fmt.Sprintf("%s notes[%d]", where, j)
			if strings.TrimSpace(n.At) == "" {
				add("%s: at is required", nw)
			}
			if n.File != "" {
				if err := CheckPath(n.File); err != nil {
					add("%s: %v", nw, err)
				}
			}
			if n.Nth < 0 {
				add("%s: nth must be positive", nw)
			}
			if !oneOf(n.Kind, NoteKinds) {
				add("%s: kind must be one of %s", nw, strings.Join(NoteKinds, ", "))
			}
			if !oneOf(n.Confidence, Confidences) {
				add("%s: confidence must be one of %s", nw, strings.Join(Confidences, ", "))
			}
		}
	}
	for i, t := range r.Tests {
		if err := CheckPath(t.File); err != nil {
			add("tests[%d]: %v", i, err)
		}
	}
	return errors.Join(errs...)
}

func CheckPath(p string) error {
	if p == "" {
		return errors.New("file is required")
	}
	if !filepath.IsLocal(filepath.FromSlash(p)) {
		return fmt.Errorf("file %q must be a relative path inside the repo", p)
	}
	return nil
}

func (r *Review) Dir() string { return filepath.Dir(r.Path) }

func (r *Review) RepoDir() string { return r.resolve(r.Repo) }

func (r *Review) BaseDirPath() string {
	if r.BaseDir == "" {
		return ""
	}
	return r.resolve(r.BaseDir)
}

func (r *Review) resolve(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(r.Dir(), p)
	}
	return filepath.Clean(p)
}

func (r *Review) Station(id string) (int, *Station) {
	for i := range r.Stations {
		if r.Stations[i].ID == id {
			return i, &r.Stations[i]
		}
	}
	return -1, nil
}

func StatePath(reviewPath string) string {
	if base, ok := strings.CutSuffix(reviewPath, ".review.yaml"); ok {
		return base + ".review.state.yaml"
	}
	return strings.TrimSuffix(reviewPath, filepath.Ext(reviewPath)) + ".state.yaml"
}

func (p Part) Label() string {
	switch {
	case p.Hunks:
		return "changes"
	case p.Func != "":
		return p.Func
	case p.From != "" && p.To != "":
		return p.From + " … " + p.To
	case p.From != "":
		return p.From
	case p.Lines != "":
		return "lines " + p.Lines
	}
	return "whole file"
}

func (n Note) Key(station string) string {
	k := station + " · "
	if n.File != "" {
		k += n.File + " · "
	}
	k += strings.TrimSpace(n.At)
	if n.Nth > 0 {
		k += fmt.Sprintf(" #%d", n.Nth)
	}
	return k
}
