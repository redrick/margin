package target

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/redrick/margin/internal/config"
	"github.com/redrick/margin/internal/review"
)

func TestParseNameStatus(t *testing.T) {
	out := []byte("M\x00a.go\x00R100\x00old/x.go\x00new/x.go\x00D\x00gone.py\x00A\x00b c.txt\x00")
	want := []Change{
		{Status: 'M', Path: "a.go"},
		{Status: 'R', Path: "new/x.go", OldPath: "old/x.go"},
		{Status: 'D', Path: "gone.py"},
		{Status: 'A', Path: "b c.txt"},
	}
	if got := ParseNameStatus(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestNewReviewIsValid(t *testing.T) {
	tg := &Target{
		Mode:  Branch,
		Repo:  "/src/app",
		Name:  "feature/login",
		Base:  strings.Repeat("a", 40),
		Head:  strings.Repeat("b", 40),
		Title: "Branch feature/login",
		Changes: []Change{
			{Status: 'M', Path: "cmd/main.go"},
			{Status: 'M', Path: "tool/main.go"},
			{Status: 'D', Path: "overview.md"},
			{Status: 'R', Path: "new.go", OldPath: "old.go"},
			{Status: 'A', Path: ".gitignore"},
		},
	}
	sts := New(tg).Stations
	var ids []string
	for _, s := range sts {
		ids = append(ids, s.ID)
		if !s.Parts[0].Hunks {
			t.Errorf("%s: parts should use hunks", s.ID)
		}
	}
	if want := []string{"main", "main-2", "file-overview", "new", "gitignore"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
	if sts[2].Parts[0].Side != review.SideBase {
		t.Errorf("deleted file should show the base side")
	}
	if sts[3].Parts[0].BaseFile != "old.go" {
		t.Errorf("rename should keep the old path")
	}

	data, err := marshal(New(tg), Location{Notes: true, Project: "app", Topic: "feature-login"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "# ---\n# title: Branch feature/login\n") {
		t.Errorf("vault review should start with frontmatter:\n%s", data)
	}
	if _, err := review.Parse("x.review.yaml", data); err != nil {
		t.Fatalf("generated review does not validate: %v\n%s", err, data)
	}
}

func TestLocate(t *testing.T) {
	home := t.TempDir()
	for k, v := range map[string]string{"HOME": home, "MARGIN_DIR": "", "MARGIN_CONFIG": filepath.Join(home, "config.yaml"), "XDG_STATE_HOME": ""} {
		t.Setenv(k, v)
	}
	repo := filepath.Join(t.TempDir(), "my app")
	tg := &Target{Mode: Branch, Repo: repo, Name: "feature/login"}

	loc := Locate(tg)
	if want := filepath.Join(repo, ".margin", "reviews", "branch-feature-login.review.yaml"); loc.Path != want || loc.Notes {
		t.Fatalf("default location = %+v, want %s", loc, want)
	}

	set := func(c config.Config) {
		t.Helper()
		if err := c.Save(); err != nil {
			t.Fatal(err)
		}
	}
	set(config.Config{Reviews: config.InState})
	if loc = Locate(tg); !strings.HasPrefix(loc.Path, filepath.Join(home, ".local", "state", "margin", "my-app-")) ||
		!strings.HasSuffix(loc.Path, "branch-feature-login.review.yaml") {
		t.Fatalf("state location = %+v", loc)
	}

	set(config.Config{Reviews: "~/notes/{project}/topics/{topic}", AgentNote: "File it under {project}/{topic}."})
	loc = Locate(tg)
	want := filepath.Join(home, "notes", "my app", "topics", "feature-login", "margin-branch-feature-login.review.yaml")
	if loc.Path != want || !loc.Notes || loc.Project != "my app" || loc.Topic != "feature-login" || loc.AgentNote != "File it under my app/feature-login." {
		t.Fatalf("notes location = %+v, want path %s", loc, want)
	}
	if back := LocationOf(want); !back.Notes || back.Project != "my app" || back.Topic != "feature-login" {
		t.Fatalf("LocationOf(%s) = %+v", want, back)
	}
	if got := FileName("feature/login", "", false); got != "margin-branch-feature-login.review.yaml" {
		t.Fatalf("FileName = %s", got)
	}

	t.Setenv("MARGIN_DIR", "/reviews")
	if loc = Locate(tg); !strings.HasPrefix(loc.Path, "/reviews/my-app-") || loc.Notes {
		t.Fatalf("MARGIN_DIR should win, got %+v", loc)
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{"core": "core", " x \n": "x", "..": "", ".": "", "a/b": "", "": ""} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatMessages(t *testing.T) {
	if asked, from := formatMessages([]string{"", "Fix login\n\nSessions expired early.\n"}); asked != "Fix login\n\nSessions expired early." || from != "the commit message" {
		t.Errorf("one commit = %q, %q", asked, from)
	}
	asked, from := formatMessages([]string{"", "Add log\n\nWhy:\nsupport asked\n", "Sort report\n"})
	want := "- Add log\n\n  Why:\n  support asked\n- Sort report"
	if asked != want || from != "2 commit messages" {
		t.Errorf("two commits =\n%s\nfrom %q, want\n%s", asked, from, want)
	}
	many := make([]string, maxCommits+5)
	for i := range many {
		many[i] = fmt.Sprintf("commit %d", i)
	}
	asked, from = formatMessages(many)
	if !strings.HasPrefix(asked, "(5 older commits left out)\n- commit 5\n") || from != fmt.Sprintf("%d commit messages", maxCommits+5) {
		t.Errorf("a long branch keeps the newest commits: %q, %q", asked[:60], from)
	}
	if asked, _ := formatMessages([]string{"bad \x1b[31mred\x1b[0m"}); strings.Contains(asked, "\x1b") {
		t.Errorf("commit messages must be sanitized, got %q", asked)
	}
}

func TestPRRef(t *testing.T) {
	for ref, ok := range map[string]bool{
		"12": true, "#12": true, "https://github.com/o/r/pull/12": true,
		"12; rm -rf /": false, "--repo=x": false, "main": false, "": false,
	} {
		if prRef.MatchString(ref) != ok {
			t.Errorf("prRef(%q) = %v, want %v", ref, !ok, ok)
		}
	}
}

func TestStagedGetsItsOwnFile(t *testing.T) {
	base := strings.Repeat("a", 40)
	work := fileName(&Target{Mode: Worktree, Base: base}, false)
	staged := fileName(&Target{Mode: Worktree, Base: base, Staged: true}, false)
	if work == staged || !strings.HasPrefix(staged, "staged-") {
		t.Errorf("staged and unstaged reviews must not share a file: %s, %s", work, staged)
	}
}
