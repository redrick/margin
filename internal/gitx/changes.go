package gitx

import (
	"sort"
	"strings"
)

type Change struct {
	Status  byte
	Path    string
	OldPath string
}

// Changes lists the files that differ between base and head. An empty head means the working tree
// including untracked files, or the index when staged is set.
func Changes(repo, base, head string, staged bool) ([]Change, error) {
	args := []string{"diff", "--name-status", "-z", "-M", "--no-ext-diff", "--no-textconv"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, base)
	if head != "" {
		args = append(args, head)
	}
	out, err := Run(repo, args...)
	if err != nil {
		return nil, err
	}
	list := ParseNameStatus(out)
	if head == "" && !staged {
		others, err := Run(repo, "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		for _, p := range strings.Split(string(others), "\x00") {
			if p != "" {
				list = append(list, Change{Status: 'A', Path: p})
			}
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	return list, nil
}

// ParseNameStatus parses `git diff --name-status -z` output.
func ParseNameStatus(out []byte) []Change {
	f := strings.Split(string(out), "\x00")
	var list []Change
	for i := 0; i < len(f); i++ {
		if f[i] == "" {
			continue
		}
		c := Change{Status: f[i][0]}
		switch c.Status {
		case 'R', 'C':
			if i+2 >= len(f) {
				return list
			}
			c.OldPath, c.Path = f[i+1], f[i+2]
			i += 2
		default:
			if i+1 >= len(f) {
				return list
			}
			c.Path = f[i+1]
			i++
		}
		list = append(list, c)
	}
	return list
}
