package testrun

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRunPattern(t *testing.T) {
	cases := map[string][]string{
		"^TestA$":                 {"TestA"},
		"^(TestA|TestB)$":         {"TestA", "TestB"},
		"^TestA$/^(one|two\\.x)$": {"TestA/one", "TestA/two.x"},
		"^TestA$/^deep$/^(x|y)$":  {"TestA/deep/x", "TestA/deep/y"},
	}
	for want, names := range cases {
		if got := RunPattern(names); got != want {
			t.Errorf("RunPattern(%v) = %q, want %q", names, got, want)
		}
	}
}

func TestPlanGroups(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/m\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []Test{
		{Name: "TestA", Package: "example.com/m/store"},
		{Name: "TestB", Package: "example.com/m/store"},
		{Name: "TestC/one", Package: "example.com/m/store"},
		{Name: "TestC/two", Package: "example.com/m/store"},
		{Name: "TestB/sub", Package: "example.com/m/store"},
		{Name: "TestD", Package: "example.com/m/api", File: "api/x_test.go"},
		{Name: "TestE", Package: "./rel"},
	}
	jobs, err := Plan("go test {dir} -run {run}", repo, tests)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, j := range jobs {
		got = append(got, j.Command)
	}
	want := []string{
		"go test ./store -run '^(TestA|TestB)$'",
		"go test ./store -run '^TestC$/^(one|two)$'",
		"go test ./api -run '^TestD$'",
		"go test ./rel -run '^TestE$'",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPlanQuotesNames(t *testing.T) {
	jobs, err := Plan("pytest {tests}", t.TempDir(), []Test{{Name: "tests/x.py::test_it's"}, {Name: "a; rm -rf ~"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Command != `pytest 'tests/x.py::test_it'\''s' 'a; rm -rf ~'` {
		t.Fatalf("got %+v", jobs)
	}
}

func TestPlanNeedsCommand(t *testing.T) {
	if _, err := Plan(" ", "", []Test{{Name: "TestA"}}); err == nil {
		t.Fatal("want an error without a command")
	}
}

func TestParseLine(t *testing.T) {
	r, ok := ParseLine("    --- FAIL: TestC/two (0.00s)")
	if !ok || r != (Result{Test: "TestC/two", Status: "fail"}) {
		t.Fatalf("got %+v %v", r, ok)
	}
	if _, ok := ParseLine("=== RUN TestA"); ok {
		t.Fatal("RUN is not a result")
	}
}

func TestRunnerRuns(t *testing.T) {
	var out []string
	var codes []int
	var r Runner
	r.Run(t.TempDir(), []Job{{Command: "echo '--- PASS: TestA (0.00s)'"}, {Command: "exit 3"}}, Events{
		Start:  func(int, Job) {},
		Output: func(_ int, s string) { out = append(out, s) },
		Result: func(_ int, res Result) { out = append(out, res.Status+" "+res.Test) },
		End:    func(_ int, code int, err error) { codes = append(codes, code) },
	})
	if !reflect.DeepEqual(out, []string{"--- PASS: TestA (0.00s)", "pass TestA"}) || !reflect.DeepEqual(codes, []int{0, 3}) {
		t.Fatalf("out %q codes %v", out, codes)
	}
}

func TestRunnerStop(t *testing.T) {
	var r Runner
	var errs []error
	done := make(chan struct{})
	go func() {
		r.Run(t.TempDir(), []Job{{Command: "sleep 30 & sleep 30"}, {Command: "echo never"}}, Events{
			Start:  func(int, Job) {},
			Output: func(_ int, s string) { t.Errorf("unexpected output %q", s) },
			Result: func(int, Result) {},
			End:    func(_ int, _ int, err error) { errs = append(errs, err) },
		})
		close(done)
	}()
	time.Sleep(200 * time.Millisecond)
	r.Stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not end the run")
	}
	if len(errs) != 2 || errs[0] == nil || errs[1] == nil {
		t.Fatalf("errs %v", errs)
	}
}
