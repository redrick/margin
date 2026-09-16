package source

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/redrick/margin/internal/gitx"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/text"
)

const maxFileSize = 8 << 20

type File struct {
	Content string
	Exists  bool
}

type Files struct {
	Repo     string
	BaseDesc string
	HeadDesc string

	root    *os.Root
	head    string
	base    reader
	current map[string]File
	baseC   map[string]File
}

type reader interface {
	read(path string) (File, error)
}

func Open(r *review.Review) (*Files, error) {
	repo := r.RepoDir()
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, fmt.Errorf("repo: %w", err)
	}
	f := &Files{Repo: repo, root: root, current: map[string]File{}, baseC: map[string]File{}}
	if err := f.init(r); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (f *Files) init(r *review.Review) error {
	switch {
	case r.BaseDir != "":
		br, err := os.OpenRoot(r.BaseDirPath())
		if err != nil {
			return fmt.Errorf("base_dir: %w", err)
		}
		f.base = dirReader{br}
		f.BaseDesc = "directory " + r.BaseDir
	case r.Base != "":
		sha, err := ResolveRef(f.Repo, r.Base)
		if err != nil {
			return err
		}
		f.base = gitReader{repo: f.Repo, sha: sha}
		f.BaseDesc = describe(r.Base, sha)
	}
	if r.Head != "" {
		sha, err := gitx.RevParse(f.Repo, r.Head)
		if err != nil {
			return err
		}
		f.head = sha
		f.HeadDesc = describe(r.Head, sha)
	}
	return nil
}

func describe(ref, sha string) string {
	switch {
	case sha == gitx.EmptyTree:
		return "empty tree"
	case ref == sha:
		return gitx.Short(sha)
	}
	return fmt.Sprintf("%s (%s)", ref, gitx.Short(sha))
}

func (f *Files) Close() {
	f.root.Close()
	if d, ok := f.base.(dirReader); ok {
		d.root.Close()
	}
}

func (f *Files) HasBase() bool { return f.base != nil }

// ReadOnly reports whether the current side comes from a commit rather than the working tree.
func (f *Files) ReadOnly() bool { return f.head != "" }

func (f *Files) Current(path string) (File, error) {
	if c, ok := f.current[path]; ok {
		return c, nil
	}
	var c File
	var err error
	if f.head != "" {
		c, err = gitReader{repo: f.Repo, sha: f.head}.read(path)
	} else {
		c, err = readRoot(f.root, path)
	}
	if err != nil {
		return File{}, err
	}
	f.current[path] = c
	return c, nil
}

func (f *Files) Base(path string) (File, error) {
	if f.base == nil {
		return File{}, errors.New("review has no base")
	}
	if c, ok := f.baseC[path]; ok {
		return c, nil
	}
	c, err := f.base.read(path)
	if err != nil {
		return File{}, err
	}
	f.baseC[path] = c
	return c, nil
}

func readRoot(root *os.Root, path string) (File, error) {
	if err := review.CheckPath(path); err != nil {
		return File{}, err
	}
	fh, err := root.Open(filepath.FromSlash(path))
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, nil
	}
	if err != nil {
		return File{}, err
	}
	defer fh.Close()
	data, err := io.ReadAll(io.LimitReader(fh, maxFileSize+1))
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	return decode(path, data)
}

func decode(path string, data []byte) (File, error) {
	if len(data) > maxFileSize {
		return File{}, fmt.Errorf("%s: larger than %d MB", path, maxFileSize>>20)
	}
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return File{}, fmt.Errorf("%s: binary file", path)
	}
	return File{Content: text.Sanitize(string(data)), Exists: true}, nil
}

type dirReader struct{ root *os.Root }

func (d dirReader) read(path string) (File, error) { return readRoot(d.root, path) }

type gitReader struct{ repo, sha string }

func (g gitReader) read(path string) (File, error) {
	if err := review.CheckPath(path); err != nil {
		return File{}, err
	}
	data, ok, err := gitx.ReadBlob(g.repo, g.sha, filepath.ToSlash(path))
	if err != nil || !ok {
		return File{}, err
	}
	return decode(path, data)
}

func ResolveRef(repo, ref string) (string, error) {
	other, ok := strings.CutPrefix(ref, review.MergeBasePrefix)
	if !ok {
		return gitx.RevParse(repo, ref)
	}
	head, err := gitx.RevParse(repo, "HEAD")
	if err != nil {
		return "", err
	}
	o, err := gitx.RevParse(repo, other)
	if err != nil {
		return "", err
	}
	return gitx.MergeBase(repo, head, o)
}
