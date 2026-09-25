package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/redrick/margin/internal/control"
	"github.com/redrick/margin/internal/coverage"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/text"
	"github.com/redrick/margin/internal/tmuxx"
)

const coverageUsage = `usage:
  margin coverage go [--review FILE]            write a script that measures Go coverage per test
  margin coverage add-go [--review FILE] <dir>  record the profiles that script collected
  margin coverage add [--review FILE] [--replace] <file.json | ->   record coverage from any tool
  margin coverage clear [--review FILE]`

var marginExe = os.Executable

func cmdCoverage(args []string) error {
	if len(args) == 0 {
		return errors.New(coverageUsage)
	}
	switch args[0] {
	case "go":
		return coverageGo(args[1:])
	case "add-go":
		return coverageAddGo(args[1:])
	case "add":
		return coverageAdd(args[1:])
	case "clear":
		return coverageClear(args[1:])
	}
	return errors.New(coverageUsage)
}

func coverageReview(name string, args []string, extra func(*flag.FlagSet)) (sock string, r *review.Review, pos []string, err error) {
	fs := flag.NewFlagSet("coverage "+name, flag.ContinueOnError)
	reviewFlag := fs.String("review", "", "")
	if extra != nil {
		extra(fs)
	}
	if pos, err = parseArgs(fs, args); err != nil {
		return
	}
	var p string
	if sock, p, err = locate(*reviewFlag); err != nil {
		return
	}
	r, err = review.Load(p)
	return
}

// reviewFiles lists the current-side files the review shows.
func reviewFiles(r *review.Review) map[string]bool {
	out := map[string]bool{}
	for _, s := range r.Stations {
		for _, p := range s.Parts {
			if p.Side != review.SideBase {
				out[p.File] = true
			}
		}
	}
	for _, t := range r.Tests {
		out[t.File] = true
	}
	return out
}

// goModules finds the module each Go file belongs to, by walking up to the nearest go.mod.
func goModules(repo string, files map[string]bool) (map[string][]string, []coverage.Module) {
	pkgs := map[string]map[string]bool{}
	var mods []coverage.Module
	seen := map[string]bool{}
	for f := range files {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		dir := path.Dir(f)
		for d := dir; ; d = path.Dir(d) {
			data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(d), "go.mod"))
			if err == nil {
				if !seen[d] {
					seen[d] = true
					mods = append(mods, coverage.Module{Path: coverage.ModulePath(data), Dir: d})
				}
				rel := "."
				if dir != d {
					rel = "./" + strings.TrimPrefix(dir, d+"/")
				}
				if pkgs[d] == nil {
					pkgs[d] = map[string]bool{}
				}
				pkgs[d][rel] = true
				break
			}
			if d == "." || d == "/" {
				break
			}
		}
	}
	out := map[string][]string{}
	for d, set := range pkgs {
		for p := range set {
			out[d] = append(out[d], p)
		}
		sort.Strings(out[d])
	}
	return out, mods
}

const goScriptHead = `#!/bin/sh
# Written by margin coverage go. Runs each test of the packages below on its own, with coverage of
# the changed packages, then hands the profiles to margin. Add packages whose tests exercise this
# change from elsewhere to a run_pkgs line.
set -u
out=$(mktemp -d)
bin="$out/pkg.test"
n=0
run_pkgs() {
	dir=$1 cover=$2
	shift 2
	cd "$dir" || return
	for pkg in "$@"; do
		rm -f "$bin"
		go test -c -cover -coverpkg="$cover" -o "$bin" "$pkg" || continue
		[ -x "$bin" ] || continue
		for t in $(cd "$pkg" && "$bin" -test.v -test.run . -test.count=1 2>/dev/null | sed -n 's/^=== RUN   //p' | sort -u); do
			n=$((n + 1))
			printf '%s\t%s\n' "$pkg" "$t" >"$out/$n.name"
			re=$(printf '%s' "$t" | sed 's/[][\\.*^$+?(){}|]/\\&/g; s#/#$/^#g; s/^/^/; s/$/$/')
			(cd "$pkg" && "$bin" -test.run "$re" -test.count=1 -test.coverprofile "$out/$n.out" >/dev/null 2>&1)
		done
	done
}
`

func coverageGo(args []string) error {
	_, r, _, err := coverageReview("go", args, nil)
	if err != nil {
		return err
	}
	repo := r.RepoDir()
	pkgs, _ := goModules(repo, reviewFiles(r))
	if len(pkgs) == 0 {
		return errors.New("the review shows no Go files inside a module; use margin coverage add for other languages")
	}
	exe, err := marginExe()
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(goScriptHead)
	var dirs []string
	for d := range pkgs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var all []string
	for _, d := range dirs {
		quoted := make([]string, len(pkgs[d]))
		for i, p := range pkgs[d] {
			quoted[i] = tmuxx.Quote(p)
		}
		fmt.Fprintf(&b, "run_pkgs %s %s %s\n", tmuxx.Quote(filepath.Join(repo, filepath.FromSlash(d))),
			tmuxx.Quote(strings.Join(pkgs[d], ",")), strings.Join(quoted, " "))
		all = append(all, pkgs[d]...)
	}
	fmt.Fprintf(&b, "%s coverage add-go --review %s \"$out\"\nstatus=$?\nrm -rf \"$out\"\nexit $status\n", tmuxx.Quote(exe), tmuxx.Quote(r.Path))

	script := strings.TrimSuffix(review.CoveragePath(r.Path), ".json") + ".sh"
	if err := os.WriteFile(script, []byte(b.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", script)
	fmt.Printf("It runs every test in %s one at a time, measuring which lines each one runs, then records the result.\n", strings.Join(all, " "))
	fmt.Printf("Read it, then run: sh %s\n", tmuxx.Quote(script))
	if r.Head != "" {
		fmt.Println("The review shows a commit, but tests run on the working tree: files that differ from the reviewed code show as stale.")
	}
	return nil
}

func coverageAddGo(args []string) error {
	sock, r, pos, err := coverageReview("add-go", args, nil)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New(coverageUsage)
	}
	dir := pos[0]
	repo := r.RepoDir()
	keep := reviewFiles(r)
	_, mods := goModules(repo, keep)
	names, err := filepath.Glob(filepath.Join(dir, "*.name"))
	if err != nil {
		return err
	}
	sort.Slice(names, func(i, j int) bool { return nameIndex(names[i]) < nameIndex(names[j]) })
	in := &coverage.Set{Executable: map[string][]int{}}
	exec := map[string]map[int]bool{}
	missing := 0
	for _, n := range names {
		data, err := os.ReadFile(n)
		if err != nil {
			return err
		}
		pkg, name, ok := strings.Cut(strings.TrimSpace(string(data)), "\t")
		if !ok {
			return fmt.Errorf("%s: want package<TAB>test", n)
		}
		f, err := os.Open(strings.TrimSuffix(n, ".name") + ".out")
		if err != nil {
			missing++
			continue
		}
		blocks, err := coverage.ParseProfile(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		t, ex := coverage.FromBlocks(name, pkg, blocks, mods, keep)
		in.Tests = append(in.Tests, t)
		for file, lines := range ex {
			if exec[file] == nil {
				exec[file] = map[int]bool{}
			}
			for l := range lines {
				exec[file][l] = true
			}
		}
	}
	if len(in.Tests) == 0 {
		return fmt.Errorf("no test profiles in %s", dir)
	}
	for file, lines := range exec {
		in.Executable[file] = coverage.SortedLines(lines)
	}
	if missing > 0 {
		fmt.Printf("%d tests left no profile (a crash or a build failure) and are not recorded\n", missing)
	}
	return saveCoverage(sock, r, in, coverage.SourceGo, true)
}

func nameIndex(p string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(p), ".name"))
	return n
}

func coverageAdd(args []string) error {
	var replace *bool
	sock, r, pos, err := coverageReview("add", args, func(fs *flag.FlagSet) { replace = fs.Bool("replace", false, "") })
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New(coverageUsage)
	}
	var data []byte
	if pos[0] == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(pos[0])
	}
	if err != nil {
		return err
	}
	in, err := coverage.Parse(data)
	if err != nil {
		return err
	}
	return saveCoverage(sock, r, in, "agent", *replace)
}

func saveCoverage(sock string, r *review.Review, in *coverage.Set, src string, replace bool) error {
	in.Hashes = map[string]string{}
	for _, f := range in.Files() {
		if file, err := source.WorktreeFile(r.RepoDir(), f); err == nil && file.Exists {
			in.Hashes[f] = coverage.Hash(text.Lines(file.Content))
		}
	}
	p := review.CoveragePath(r.Path)
	set, err := coverage.Load(p)
	if err != nil {
		return err
	}
	if set == nil {
		set = &coverage.Set{}
	}
	set.Merge(in, src, replace)
	if err := set.Save(p); err != nil {
		return err
	}
	fmt.Printf("recorded %d tests in %s\n", len(in.Tests), p)
	if err := printUntested(r, set); err != nil {
		return err
	}
	return reloadViewer(sock)
}

// printUntested tells the agent which changed lines no test runs, the ones worth a note.
func printUntested(r *review.Review, set *coverage.Set) error {
	files, err := source.Open(r)
	if err != nil {
		return err
	}
	d := doc.Build(r, files)
	files.Close()
	d.AttachCoverage(set)
	c := d.Coverage
	var run, missed int
	var lines, stale []string
	seen := map[string]bool{}
	for _, st := range d.Stations {
		if st.Kind != doc.Code {
			continue
		}
		sc := st.Coverage(c, -1)
		run, missed = run+sc.Run, missed+sc.Missed
		for _, p := range st.Parts {
			if c != nil && c.Stale[p.Spec.File] && !seen["stale "+p.Spec.File] {
				seen["stale "+p.Spec.File] = true
				stale = append(stale, p.Spec.File)
			}
			for i := p.Range.Start; i <= p.Range.End && i < len(p.Lines); i++ {
				k := fmt.Sprintf("%s:%d", p.Spec.File, i+1)
				if c.Line(p, i) == doc.CovMissed && !seen[k] {
					seen[k] = true
					lines = append(lines, fmt.Sprintf("  %s  (%s)  %s", k, st.ID, doc.Short(p.Lines[i], 60)))
				}
			}
		}
	}
	fmt.Printf("%d of %d changed lines are run by a test\n", run, run+missed)
	if len(stale) > 0 {
		fmt.Printf("stale, the working tree differs from the reviewed code: %s\n", strings.Join(stale, ", "))
	}
	if len(lines) > 0 {
		fmt.Println("changed lines no test runs:")
		for i, l := range lines {
			if i == 30 {
				fmt.Printf("  … and %d more\n", len(lines)-30)
				break
			}
			fmt.Println(l)
		}
	}
	return nil
}

func coverageClear(args []string) error {
	sock, r, _, err := coverageReview("clear", args, nil)
	if err != nil {
		return err
	}
	if err := os.Remove(review.CoveragePath(r.Path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Println("coverage cleared")
	return reloadViewer(sock)
}

func reloadViewer(sock string) error {
	if sock == "" {
		return nil
	}
	_, err := control.Call(sock, control.Request{Cmd: "reload"})
	return err
}
