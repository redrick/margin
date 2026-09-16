package doc

import (
	"path/filepath"
	"regexp"
	"strings"
)

type TestGroup struct {
	File    string
	Why     string
	Names   []TestName
	Err     error
	NewFile bool
	New     map[string]bool
}

type TestName struct {
	Name  string
	Depth int
}

var (
	goTopTest = regexp.MustCompile(`^func ((?:Test|Benchmark|Fuzz|Example)\w*)\(`)
	goSubTest = regexp.MustCompile(`^(\t*).*\b\w+\.Run\(\s*"((?:[^"\\]|\\.)*)"`)
	pyTest    = regexp.MustCompile(`^(\s*)(?:async\s+)?(?:def (test\w*)\(|class (Test\w*))`)
	jsTest    = regexp.MustCompile("^(\\s*)(?:describe|it|test|context)(?:\\.\\w+)?\\(\\s*['\"`](.*?)['\"`]")
	rbTest    = regexp.MustCompile(`^(\s*)(?:(?:RSpec\.)?describe|context|it|test)\s+['"](.*?)['"]|^(\s*)def (test_\w+)`)
)

func ParseTests(path string, lines []string) []TestName {
	var out []TestName
	ext := strings.ToLower(filepath.Ext(path))
	for _, l := range lines {
		switch ext {
		case ".go":
			if m := goTopTest.FindStringSubmatch(l); m != nil {
				out = append(out, TestName{m[1], 0})
			} else if m := goSubTest.FindStringSubmatch(l); m != nil {
				out = append(out, TestName{m[2], max(len(m[1]), 1)})
			}
		case ".py":
			if m := pyTest.FindStringSubmatch(l); m != nil {
				out = append(out, TestName{m[2] + m[3], len(m[1]) / 4})
			}
		case ".rb":
			if m := rbTest.FindStringSubmatch(l); m != nil {
				if m[2] != "" {
					out = append(out, TestName{m[2], len(m[1]) / 2})
				} else {
					out = append(out, TestName{m[4], len(m[3]) / 2})
				}
			}
		default:
			if m := jsTest.FindStringSubmatch(l); m != nil {
				out = append(out, TestName{m[2], len(m[1]) / 2})
			}
		}
	}
	return out
}
