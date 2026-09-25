package source

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/redrick/margin/internal/gitx"
)

// Changed lists the files that differ between the base and the current side. A review without a
// base has nothing to compare, so it lists nothing.
func (f *Files) Changed() ([]gitx.Change, error) {
	switch d := f.base.(type) {
	case gitReader:
		return gitx.Changes(f.Repo, f.baseSHA, f.head, f.staged)
	case dirReader:
		return dirChanges(d.root, f.root)
	}
	return nil, nil
}

func dirChanges(base, cur *os.Root) ([]gitx.Change, error) {
	old, err := listFiles(base)
	if err != nil {
		return nil, err
	}
	now, err := listFiles(cur)
	if err != nil {
		return nil, err
	}
	var out []gitx.Change
	for p := range now {
		if _, ok := old[p]; !ok {
			out = append(out, gitx.Change{Status: 'A', Path: p})
			continue
		}
		a, errA := fs.ReadFile(base.FS(), p)
		b, errB := fs.ReadFile(cur.FS(), p)
		if errA != nil || errB != nil || !bytes.Equal(a, b) {
			out = append(out, gitx.Change{Status: 'M', Path: p})
		}
	}
	for p := range old {
		if _, ok := now[p]; !ok {
			out = append(out, gitx.Change{Status: 'D', Path: p})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func listFiles(root *os.Root) (map[string]bool, error) {
	files := map[string]bool{}
	err := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && d.Name() == ".git":
			return filepath.SkipDir
		case d.Type().IsRegular():
			files[p] = true
		}
		return nil
	})
	return files, err
}

const configDir = ".margin/"

// RepoConfig reads a file under the repository's .margin directory. It comes from the base side
// when there is one, so a change under review cannot rewrite the rules it is reviewed by.
func (f *Files) RepoConfig(name string) string {
	path := configDir + name
	if f.HasBase() {
		if c, err := f.Base(path); err == nil && c.Exists {
			return c.Content
		}
		return ""
	}
	if c, err := f.Current(path); err == nil && c.Exists {
		return c.Content
	}
	return ""
}
