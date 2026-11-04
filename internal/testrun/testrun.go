// Package testrun turns tests of a review's coverage into shell commands, from a command template
// the user set up for the repository, and runs them.
package testrun

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/redrick/margin/internal/coverage"
)

// GoDefault is offered when a repository has Go tests and no command yet.
const GoDefault = "go test -count=1 -v {dir} -run {run}"

// Placeholders lists what a command template may use.
var Placeholders = map[string]string{
	"{pkg}":   "the package (Go import path), or the test's group",
	"{dir}":   "the package's folder inside the repository, like ./internal/store",
	"{run}":   "a go test -run pattern matching exactly the picked tests",
	"{tests}": "the picked tests' names, separated by spaces",
	"{file}":  "the file the first picked test is defined in",
}

type Test struct {
	Name    string
	Package string
	Group   string
	File    string
}

// Job is one command: the picked tests of one package, or one top-level test with its subtests.
type Job struct {
	Label   string   `json:"label"`
	Command string   `json:"command"`
	Tests   []string `json:"tests"`
}

// Plan groups tests into jobs and fills the template for each. Every filled-in value is shell-quoted.
func Plan(template, repo string, tests []Test) ([]Job, error) {
	if strings.TrimSpace(template) == "" {
		return nil, errors.New("no test command set up for this repository")
	}
	tests = dropCovered(tests)
	mod := rootModule(repo)
	type group struct {
		key   string
		tests []Test
	}
	var groups []*group
	byKey := map[string]*group{}
	for _, t := range tests {
		pkg := t.Package
		if pkg == "" {
			pkg = t.Group
		}
		key := pkg
		if top, rest, ok := strings.Cut(t.Name, "/"); ok && rest != "" && t.Package != "" {
			key = pkg + "\x00" + top
		}
		g := byKey[key]
		if g == nil {
			g = &group{key: key}
			byKey[key] = g
			groups = append(groups, g)
		}
		g.tests = append(g.tests, t)
	}
	var jobs []Job
	for _, g := range groups {
		first := g.tests[0]
		pkg := first.Package
		if pkg == "" {
			pkg = first.Group
		}
		var names []string
		for _, t := range g.tests {
			names = append(names, t.Name)
		}
		quoted := make([]string, len(names))
		for i, n := range names {
			quoted[i] = Quote(n)
		}
		cmd := strings.NewReplacer(
			"{pkg}", Quote(pkg),
			"{dir}", Quote(dirOf(first, mod)),
			"{run}", Quote(RunPattern(names)),
			"{file}", Quote(first.File),
			"{tests}", strings.Join(quoted, " "),
		).Replace(template)
		label := pkg
		if len(names) == 1 {
			label += " " + names[0]
		} else {
			label += " · " + strings.Join(names, ", ")
		}
		jobs = append(jobs, Job{Label: label, Command: cmd, Tests: names})
	}
	return jobs, nil
}

// dropCovered removes subtests whose parent is picked too: running the parent runs them.
func dropCovered(tests []Test) []Test {
	picked := map[string]bool{}
	for _, t := range tests {
		picked[t.Package+"\x00"+t.Group+"\x00"+t.Name] = true
	}
	var out []Test
	seen := map[string]bool{}
	for _, t := range tests {
		key := t.Package + "\x00" + t.Group + "\x00" + t.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		covered := false
		for n := t.Name; t.Package != "" && strings.Contains(n, "/"); {
			n = n[:strings.LastIndex(n, "/")]
			if picked[t.Package+"\x00"+t.Group+"\x00"+n] {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, t)
		}
	}
	return out
}

// RunPattern is a go test -run pattern for names that share their top-level test, or that are all
// top-level tests.
func RunPattern(names []string) string {
	levels := [][]string{}
	for _, n := range names {
		for i, part := range strings.Split(n, "/") {
			if i == len(levels) {
				levels = append(levels, nil)
			}
			levels[i] = appendNew(levels[i], regexp.QuoteMeta(part))
		}
	}
	out := make([]string, len(levels))
	for i, alts := range levels {
		if len(alts) == 1 {
			out[i] = "^" + alts[0] + "$"
		} else {
			out[i] = "^(" + strings.Join(alts, "|") + ")$"
		}
	}
	return strings.Join(out, "/")
}

func appendNew(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func rootModule(repo string) []coverage.Module {
	data, err := os.ReadFile(filepath.Join(repo, "go.mod"))
	if err != nil {
		return nil
	}
	return []coverage.Module{{Path: coverage.ModulePath(data), Dir: "."}}
}

func dirOf(t Test, mods []coverage.Module) string {
	if t.File != "" {
		if d := path.Dir(filepath.ToSlash(t.File)); d != "." {
			return "./" + d
		}
		return "."
	}
	if t.Package == "." || strings.HasPrefix(t.Package, "./") {
		return t.Package
	}
	if t.Package != "" {
		if rel, ok := coverage.Rel(mods, t.Package+"/x"); ok {
			if d := path.Dir(rel); d != "." {
				return "./" + d
			}
			return "."
		}
	}
	return "."
}

// Quote makes s one shell word.
func Quote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@%+,") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Result is what a run's output says about a test.
type Result struct {
	Test   string `json:"test"`
	Status string `json:"status"`
}

var resultLine = regexp.MustCompile(`^\s*--- (PASS|FAIL|SKIP): (\S+)`)

// ParseLine reads a go test -v result line.
func ParseLine(line string) (Result, bool) {
	m := resultLine.FindStringSubmatch(line)
	if m == nil {
		return Result{}, false
	}
	return Result{Test: m[2], Status: strings.ToLower(m[1])}, true
}

// Runner runs jobs one after another in dir and reports their output as it comes.
type Runner struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

type Events struct {
	Start  func(i int, j Job)
	Output func(i int, text string)
	Result func(i int, r Result)
	End    func(i int, code int, err error)
}

// Run blocks until every job ran or Stop is called. A job that fails does not stop the next one.
func (r *Runner) Run(dir string, jobs []Job, ev Events) {
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	r.cancel = cancel
	r.mu.Unlock()
	defer cancel()
	for i, j := range jobs {
		if ctx.Err() != nil {
			ev.End(i, -1, context.Canceled)
			continue
		}
		ev.Start(i, j)
		code, err := runOne(ctx, dir, j.Command, func(line string) {
			ev.Output(i, line)
			if res, ok := ParseLine(line); ok {
				ev.Result(i, res)
			}
		})
		ev.End(i, code, err)
	}
}

// Stop ends the running job and skips the rest.
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
}

func runOne(ctx context.Context, dir, command string, line func(string)) (int, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	setGroup(cmd)
	cmd.WaitDelay = 2 * time.Second
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return -1, err
	}
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line(sc.Text())
		}
		io.Copy(io.Discard, pr)
		close(done)
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	if ctx.Err() != nil {
		return -1, context.Canceled
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
