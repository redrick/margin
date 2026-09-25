package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/redrick/margin/internal/coverage"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/testutil"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const calcBase = `package calc

func Add(a, b int) int { return a + b }
`

const calcCurrent = `package calc

func Add(a, b int) int {
	if a < 0 {
		return b + a
	}
	return a + b
}

func Sub(a, b int) int {
	return a - b
}
`

const calcTest = `package calc

import "testing"

func TestAdd(t *testing.T) {
	t.Run("small numbers", func(t *testing.T) {
		if Add(1, 2) != 3 {
			t.Fatal("1+2")
		}
	})
	t.Run("negative (a)", func(t *testing.T) {
		if Add(-1, 2) != 1 {
			t.Fatal("-1+2")
		}
	})
}
`

// TestCoverageGoScript runs the generated script for real on a tiny module, so it needs the go tool.
func TestCoverageGoScript(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a test binary")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go tool")
	}
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"current/go.mod":            "module example.com/cov\n\ngo 1.21\n",
		"current/calc/calc.go":      calcCurrent,
		"current/calc/calc_test.go": calcTest,
		"base/calc/calc.go":         calcBase,
		"calc.review.yaml": `version: 1
repo: current
base_dir: base
title: calc
stations:
  - id: calc
    title: Add and Sub
    parts:
      - file: calc/calc.go
`,
	})
	path := filepath.Join(dir, "calc.review.yaml")
	profiles := filepath.Join(dir, "profiles")
	stub := filepath.Join(dir, "stub.sh")
	writeFiles(t, dir, map[string]string{"stub.sh": "#!/bin/sh\ncp -r \"$5\" " + profiles + "\n"})
	if err := os.Chmod(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	marginExe = func() (string, error) { return stub, nil }
	t.Cleanup(func() { marginExe = os.Executable })

	if err := coverageGo([]string{"--review", path}); err != nil {
		t.Fatal(err)
	}
	script := strings.TrimSuffix(review.CoveragePath(path), ".json") + ".sh"
	cmd := exec.Command("sh", script)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	if err := coverageAddGo([]string{"--review", path, profiles}); err != nil {
		t.Fatal(err)
	}

	set, err := coverage.Load(review.CoveragePath(path))
	if err != nil || set == nil {
		t.Fatalf("coverage file: %v", err)
	}
	byName := map[string]coverage.Test{}
	for _, tc := range set.Tests {
		byName[tc.Name] = tc
	}
	for _, name := range []string{"TestAdd", "TestAdd/small_numbers", "TestAdd/negative_(a)"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing %s in %+v", name, set.Tests)
		}
	}
	if lines := byName["TestAdd/small_numbers"].Lines["calc/calc.go"]; slices.Contains(lines, 5) || !slices.Contains(lines, 7) {
		t.Errorf("small numbers should skip the a < 0 branch and reach line 7: %v", lines)
	}
	if lines := byName["TestAdd/negative_(a)"].Lines["calc/calc.go"]; !slices.Contains(lines, 5) {
		t.Errorf("the negative case should run line 5: %v", lines)
	}
	if lines := byName["TestAdd"].Lines["calc/calc.go"]; slices.Contains(lines, 11) {
		t.Errorf("no test calls Sub: %v", lines)
	}
	if !slices.Contains(set.Executable["calc/calc.go"], 11) || set.Hashes["calc/calc.go"] == "" {
		t.Errorf("Sub's body should be executable and the file hashed: %v %v", set.Executable, set.Hashes)
	}
}

func TestCoverageAddJSON(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	path := testutil.Example(t)
	in := filepath.Join(filepath.Dir(path), "cov.json")
	writeFiles(t, filepath.Dir(path), map[string]string{"cov.json": `{"tests": [
		{"name": "test_summarize", "file": "scripts/test_report.py", "lines": {"scripts/report.py": [12, 13]}}
	]}`})
	if code := run([]string{"coverage", "clear", "--review", path}); code != 0 {
		t.Fatal("clear failed")
	}
	if code := run([]string{"coverage", "add", "--review", path, in}); code != 0 {
		t.Fatalf("coverage add exit %d", code)
	}
	if code := run([]string{"coverage", "add", "--review", path, in}); code != 0 {
		t.Fatalf("second coverage add exit %d", code)
	}
	set, err := coverage.Load(review.CoveragePath(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tests) != 1 || set.Tests[0].Source != "agent" || set.Hashes["scripts/report.py"] == "" {
		t.Fatalf("adding the same test twice should keep one: %+v", set)
	}
	if code := run([]string{"coverage", "clear", "--review", path}); code != 0 {
		t.Fatal("clear failed")
	}
	if set, _ := coverage.Load(review.CoveragePath(path)); set != nil {
		t.Fatal("clear left the file")
	}
}
