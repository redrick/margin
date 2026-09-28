package source

import (
	"bytes"
	"io/fs"
	"regexp"
	"strconv"
	"strings"

	"github.com/redrick/margin/internal/gitx"
	"github.com/redrick/margin/internal/text"
)

type Match struct {
	File string
	Line int
	Text string
}

// maxMatches caps a search, so a very common name cannot flood the viewer.
const maxMatches = 400

// Grep searches the current side of the review for an extended regular expression, the way the
// review sees it: the working tree, the index or the head commit.
func (f *Files) Grep(pattern string) ([]Match, error) {
	if _, ok := f.base.(dirReader); ok || f.baseSHA == "" {
		return f.walkGrep(pattern)
	}
	args := []string{"grep", "-n", "-I", "-z", "-E", "-e", pattern}
	switch {
	case f.staged:
		args = append(args, "--cached")
	case f.head != "":
		args = append(args, f.head)
	}
	out, err := gitx.Run(f.Repo, append(args, "--")...)
	if err != nil {
		// git grep exits 1 when nothing matches.
		if strings.Contains(err.Error(), "exit status 1") {
			return nil, nil
		}
		return nil, err
	}
	var ms []Match
	for _, l := range strings.Split(string(out), "\n") {
		file, rest, ok := strings.Cut(l, "\x00")
		if !ok {
			continue
		}
		if f.head != "" {
			file = strings.TrimPrefix(file, f.head+":")
		}
		num, txt, ok := strings.Cut(rest, "\x00")
		if !ok {
			num, txt, ok = strings.Cut(rest, ":")
		}
		n, err := strconv.Atoi(num)
		if !ok || err != nil {
			continue
		}
		ms = append(ms, Match{File: file, Line: n, Text: text.Line(txt)})
		if len(ms) >= maxMatches {
			break
		}
	}
	return ms, nil
}

func (f *Files) walkGrep(pattern string) ([]Match, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	var ms []Match
	err = fs.WalkDir(f.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && d.Name() == ".git":
			return fs.SkipDir
		case !d.Type().IsRegular() || len(ms) >= maxMatches:
			return nil
		}
		data, err := fs.ReadFile(f.root.FS(), p)
		if err != nil || len(data) > maxFileSize || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			return nil
		}
		for i, l := range strings.Split(string(data), "\n") {
			if re.MatchString(l) {
				ms = append(ms, Match{File: p, Line: i + 1, Text: text.Line(l)})
			}
		}
		return nil
	})
	return ms, err
}

// BaseCommit is the commit the review compares against, or "" when the base is a directory.
func (f *Files) BaseCommit() string { return f.baseSHA }
