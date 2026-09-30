package coverage

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// TopName is the part of a test name that names its function or block: the first segment of a Go
// subtest path, or the last :: segment of a pytest id, without parameters.
func TopName(name string) string {
	top := name
	if i := strings.LastIndex(top, "::"); i >= 0 {
		top = top[i+2:]
	} else {
		top, _, _ = strings.Cut(top, "/")
	}
	top, _, _ = strings.Cut(top, "[")
	top, _, _ = strings.Cut(top, "|")
	return strings.TrimSpace(top)
}

// DefPattern matches the line that defines a test named top, in the languages margin reads: a Go,
// Python or Rust function, or a JavaScript or Ruby test block. It is valid as Go and as extended
// regular expression syntax, so git grep can use it too.
func DefPattern(top string) string {
	q := regexp.QuoteMeta(top)
	return `((func|def|fn)[[:space:]]+(\([^)]*\)[[:space:]]*)?` + q + `([^[:alnum:]_]|$))|((it|test|describe|context|specify)[[:space:]]*\(?[[:space:]]*['"` + "`" + `]` + q + `['"` + "`" + `])`
}

// DefLine finds where the test called name is defined in lines: the function or block named by its
// top name, then for each subtest the first later line that quotes the subtest's name. A subtest
// that cannot be found leaves its parent's line. It returns a 0-based line, or -1.
func DefLine(lines []string, name string) int {
	top := TopName(name)
	if top == "" {
		return -1
	}
	re := regexp.MustCompile(DefPattern(top))
	at := -1
	for i, l := range lines {
		if re.MatchString(l) {
			at = i
			break
		}
	}
	if at < 0 {
		return -1
	}
	_, rest, ok := strings.Cut(name, "/")
	if !ok || strings.Contains(name, "::") {
		return at
	}
	for sub := range strings.SplitSeq(rest, "/") {
		var quoted []string
		for _, v := range []string{sub, strings.ReplaceAll(sub, "_", " ")} {
			quoted = append(quoted, `"`+v+`"`, "'"+v+"'", "`"+v+"`")
		}
		found := false
		for i := at + 1; i < len(lines) && !found; i++ {
			for _, q := range quoted {
				if strings.Contains(lines[i], q) {
					at, found = i, true
					break
				}
			}
		}
		if !found {
			break
		}
	}
	return at
}

// FindGo looks for a Go test among the _test.go files of dir, a directory relative to repo. It
// returns the file relative to repo and the 1-based line, or ok false.
func FindGo(repo, dir, name string) (string, int, bool) {
	files, err := filepath.Glob(filepath.Join(repo, dir, "*_test.go"))
	if err != nil {
		return "", 0, false
	}
	sort.Strings(files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if l := DefLine(strings.Split(string(data), "\n"), name); l >= 0 {
			return filepath.ToSlash(filepath.Join(dir, filepath.Base(f))), l + 1, true
		}
	}
	return "", 0, false
}
