package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/redrick/margin/internal/cli"
	"github.com/redrick/margin/internal/coverage"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/verdict"
)

func TestPRWalkthroughHandoff(t *testing.T) {
	path, repo := prWalkFixture(t)
	r := &Reviews{path: path}
	view, err := r.Current()
	if err != nil || view.Walkthrough.Mode != fileWalk || view.Walkthrough.Ready {
		t.Fatalf("initial view = %+v, %v", view, err)
	}
	const fileID = "file:retired.txt"
	deleted := walkStop(t, view, fileID)
	if len(deleted.Parts) != 1 || !deleted.Parts[0].Base {
		t.Fatalf("deleted file lost its base side: %+v", deleted.Parts)
	}
	filePosition := ReadingPosition{Station: fileID, Mode: fileWalk, Scroll: 125,
		Step: &ReadingStep{Kind: "change", File: "retired.txt", Side: "base"}}
	if err := r.SaveReadingPosition(path, filePosition, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetVerdict(fileID, verdict.Changes); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddComment(CommentInput{Station: fileID, Line: 0, Text: "Check the old route"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SelectWalkthrough(path, agentWalk); err == nil {
		t.Fatal("an unfinished walkthrough could be selected")
	}

	log, agent := testAgent(t, "review-agent")
	t.Setenv("MARGIN_AGENT", agent)
	a := &Agents{}
	t.Cleanup(func() { _ = a.ServiceShutdown() })
	if _, err := a.Start(path, repo, false, 80, 24); err != nil {
		t.Fatal(err)
	}
	preparation := readReviewResume(path).WalkPreparation
	prompt := strings.Join(waitAgentArgs(t, log), " ")
	if preparation == "" || !strings.Contains(prompt, "walkthrough-ready") ||
		!strings.Contains(prompt, preparation) || !strings.Contains(prompt, "--new") {
		t.Fatalf("fake agent did not receive the preparation checkpoint: %q", prompt)
	}
	if !a.Running(path) || !a.ResumeStatus(path, repo).WalkAttempted {
		t.Fatal("preparing agent was not visible")
	}
	publish := func(id string) error {
		return markWalkthroughReady([]string{"--review", path, "--preparation", id})
	}

	partial := strings.Split(prWalkGrouped, "      - file: retired.txt")[0]
	writeWalkReview(t, path, partial)
	if err := publish(preparation); err == nil {
		t.Fatal("an incomplete grouping was published")
	}
	view, err = r.Current()
	if err != nil || view.Walkthrough.Ready || view.Walkthrough.Mode != fileWalk {
		t.Fatalf("partial save changed the walkthrough: %+v, %v", view, err)
	}
	walkStop(t, view, fileID)
	r.gotWalkPosition(t, path, filePosition)

	running := a.session(path)
	if err := a.Stop(path); err != nil {
		t.Fatal(err)
	}
	waitAgentExit(t, running)
	if a.Running(path) || !a.ResumeStatus(path, repo).WalkAttempted {
		t.Fatal("stopped preparation lost its status")
	}
	if _, err := a.Start(path, repo, false, 80, 24); err != nil {
		t.Fatal(err)
	}
	writeWalkReview(t, path, prWalkGrouped)
	if err := publish(preparation); err == nil {
		t.Fatal("a superseded agent published its walkthrough")
	}
	if err := publish(readReviewResume(path).WalkPreparation); err != nil {
		t.Fatal(err)
	}

	// A restart before the reader answers the offer still opens the old view and position.
	r = &Reviews{path: path, agents: a}
	view, err = r.Current()
	if err != nil || !view.Walkthrough.Ready || !view.Walkthrough.Offer || view.Walkthrough.Mode != fileWalk {
		t.Fatalf("ready view = %+v, %v", view, err)
	}
	r.gotWalkPosition(t, path, filePosition)
	view, err = r.SelectWalkthrough(path, fileWalk)
	if err != nil || view.Walkthrough.Offer {
		t.Fatalf("keep reviewing by file = %+v, %v", view, err)
	}
	view, err = r.SelectWalkthrough(path, agentWalk)
	if err != nil || view.Walkthrough.Mode != agentWalk || view.Walkthrough.Position != nil {
		t.Fatalf("first agent view = %+v, %v", view, err)
	}
	walkStop(t, view, "shipping")
	comment := view.Reader.Comments[0]
	if comment.Station != "shipping" || comment.Part != 1 || comment.At != 0 || !comment.Draft {
		t.Fatalf("comment did not follow the deleted line: %+v", comment)
	}
	if view.Reader.Outcome.Decision != verdict.RequestChanges || view.Reader.Verdicts["shipping"] != "" || view.Reader.ToFix != 1 {
		t.Fatalf("file verdict or fix request was lost or transferred to the grouped stop: %+v", view.Reader)
	}
	if _, err := r.SetVerdict("shipping", verdict.Good); err != nil {
		t.Fatal(err)
	}
	for format, want := range map[string]string{"md": "Recommendation: request changes", "github": "REQUEST_CHANGES"} {
		exported, err := r.Export(format)
		if err != nil || !strings.Contains(exported, want) {
			t.Fatalf("export %s lost the file verdict: %s, %v", format, exported, err)
		}
	}
	if _, err := r.SendFixes(); err != nil {
		t.Fatalf("send fix from the other walkthrough: %v", err)
	}
	agentPosition := ReadingPosition{Station: "shipping", Mode: agentWalk, Scroll: 42}
	if err := r.SaveReadingPosition(path, agentPosition, true); err != nil {
		t.Fatal(err)
	}

	writeWalkReview(t, path, prWalkGrouped+prWalkLateNote)
	view, err = r.Current()
	if err != nil {
		t.Fatal(err)
	}
	note := walkStop(t, view, "shipping").Notes[0]
	if _, err := r.MarkNote(note.Key, "reviewed"); err != nil {
		t.Fatal(err)
	}
	view, err = r.SelectWalkthrough(path, fileWalk)
	if err != nil {
		t.Fatal(err)
	}
	projected := walkStop(t, view, fileID).Notes
	if len(projected) != 1 || projected[0].Key != note.Key || !slices.Contains(view.Reader.Reviewed, note.Key) {
		t.Fatalf("late note or its reader mark was lost: %+v", projected)
	}
	route := walkStop(t, view, "file:route.txt")
	if len(route.Notes) != 1 || route.Notes[0].AuthoredRef != "shipping:2" ||
		route.Notes[0].Part < 0 || !route.Parts[route.Notes[0].Part].Base {
		t.Fatalf("a note on a removal inside a modified file lost its base anchor: %+v", route)
	}
	if c := view.Reader.Comments[0]; c.Station != fileID || c.Part != 0 || c.At != 0 {
		t.Fatalf("draft did not return to its original line: %+v", c)
	}
	r.gotWalkPosition(t, path, filePosition)
	if _, err := r.SendComments(); err != nil {
		t.Fatal(err)
	}
	if code := cli.Run([]string{"resolve", "--review", path, "--no-goto", "c1", "Confirmed the old route is unused."}); code != 0 {
		t.Fatalf("could not resolve a comment written in File by file: exit %d", code)
	}
	view, err = r.Current()
	if err != nil || !view.Reader.Comments[0].Resolved {
		t.Fatalf("the resolution was not visible in the file view: %+v, %v", view, err)
	}

	view, err = r.SelectWalkthrough(path, agentWalk)
	if err != nil || !view.Reader.Comments[0].Resolved {
		t.Fatalf("the resolution was not visible in the agent walkthrough: %+v, %v", view, err)
	}
	r.gotWalkPosition(t, path, agentPosition)
}

func TestLegacyPRWalkthroughRestoresItsPosition(t *testing.T) {
	path, _ := prWalkFixture(t)
	position := ReadingPosition{Station: "retired", Scroll: 12}
	if err := updateReviewResume(path, func(saved *reviewResume) bool {
		saved.Position = &position
		return true
	}); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(review.StatePath(path))
	if err != nil {
		t.Fatal(err)
	}
	st.Verdicts["retired"] = verdict.Good
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	r := &Reviews{path: path}
	view, err := r.Current()
	if err != nil || view.Walkthrough.Mode != fileWalk || view.Reader.Verdicts["file:retired.txt"] != verdict.Good ||
		view.Reader.Verdicts["retired"] != "" {
		t.Fatalf("legacy file view = %+v, %v", view, err)
	}
	r.gotWalkPosition(t, path, ReadingPosition{Station: "file:retired.txt", Mode: fileWalk, Scroll: 12})

	path, _ = prWalkFixture(t)
	writeWalkReview(t, path, prWalkGrouped)
	r = &Reviews{path: path}
	view, err = r.Current()
	if err != nil || view.Walkthrough.Mode != agentWalk || !view.Walkthrough.Ready {
		t.Fatalf("legacy authored view = %+v, %v", view, err)
	}
}

func TestLocalReviewWalkthrough(t *testing.T) {
	path, _ := prWalkFixture(t)
	for _, side := range []string{"base", "head"} {
		file := filepath.Join(filepath.Dir(path), side, "limits.txt")
		if err := os.WriteFile(file, []byte("request limit\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setLocalTitle := func() {
		if err := review.SetScalar(path, "title", "Uncommitted changes"); err != nil {
			t.Fatal(err)
		}
	}
	setLocalTitle()
	r := &Reviews{path: path}
	view, err := r.Current()
	if err != nil || view.Walkthrough == nil || view.Walkthrough.Mode != fileWalk || view.Walkthrough.Ready {
		t.Fatalf("initial local review = %+v, %v", view, err)
	}
	const fileID = "file:retired.txt"
	if _, err := r.AddComment(CommentInput{Station: fileID, Line: 0, Text: "Check the old route"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetFull(true); err != nil {
		t.Fatal(err)
	}
	rv, err := review.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	preparation, err := beginWalkPreparation(rv)
	if err != nil || preparation == "" {
		t.Fatalf("begin local walkthrough = %q, %v", preparation, err)
	}
	writeWalkReview(t, path, prWalkGrouped+`      - file: limits.txt
        lines: "1"
        background: true
    notes:
      - at: request limit
        file: limits.txt
        kind: question
        focus: performance
        text: Do we need to compare both routes under load?
`)
	setLocalTitle()
	if _, err := r.SelectWalkthrough(path, agentWalk); err == nil {
		t.Fatal("selected local walkthrough before publication")
	}
	if err := markWalkthroughReady([]string{"--review", path, "--preparation", preparation}); err != nil {
		t.Fatal(err)
	}
	view, err = r.Current()
	if err != nil || view.Walkthrough.Mode != fileWalk || !view.Walkthrough.Ready || !view.Walkthrough.Offer {
		t.Fatalf("ready local review = %+v, %v", view, err)
	}
	if got := view.Walkthrough.FocusAreas; !slices.Equal(got, []string{"performance"}) {
		t.Fatalf("file view lost the guided-only focus area: %v", got)
	}
	view, err = r.SelectWalkthrough(path, agentWalk)
	if err != nil || view.Walkthrough.Mode != agentWalk || !view.Reader.Full {
		t.Fatalf("guided view did not preserve display choice: %+v, %v", view, err)
	}
	if c := view.Reader.Comments[0]; c.Station != "shipping" || c.At != 0 {
		t.Fatalf("local comment did not follow its anchor: %+v", c)
	}
	view, err = r.SelectWalkthrough(path, fileWalk)
	if err != nil || view.Reader.Comments[0].Station != fileID || !view.Reader.Full {
		t.Fatalf("local feedback or display choice was lost on return: %+v, %v", view, err)
	}
}

func TestContinueWalkthroughWaitsForReader(t *testing.T) {
	prompt := kickoff("/tmp/example.review.yaml", false, true, "", "pending-checkpoint")
	if !strings.Contains(prompt, "--continue") || !strings.Contains(prompt, "Wait for the reader") ||
		strings.Contains(prompt, "--new") || !strings.Contains(prompt, "pending-checkpoint") {
		t.Fatalf("continuing a saved walkthrough changed the continuation contract: %q", prompt)
	}
}

func TestReviewNoteVisibility(t *testing.T) {
	path, _ := prWalkFixture(t)
	writeWalkReview(t, path, `stations:
  - id: shipping
    title: Select the priority route
    risk: high
    risk_why: Changing the route can disrupt shipping.
    parts:
      - file: route.txt
        hunks: true
    notes:
      - at: route by priority
        text: Confirm the priority.
  - id: cleanup
    title: Remove the old route
    risk: low
    parts:
      - file: retired.txt
        side: base
        hunks: true
    notes:
      - at: legacy route
        text: Remove the unused route.
`)
	r := &Reviews{path: path}
	view, err := r.Current()
	if err != nil {
		t.Fatal(err)
	}
	if view.Reader.NoteVisibility != "all" || !walkStop(t, view, "shipping").Notes[0].HighRisk {
		t.Fatal("guided review lost its default or high-risk note")
	}
	if update, err := r.SetBlind(true); err != nil || update.Reader.NoteVisibility != "hide" {
		t.Fatalf("legacy blind setting did not become Hide: %v", err)
	}
	if update, err := r.SetNoteVisibility(path, "risky"); err != nil || update.Reader.NoteVisibility != "risky" || !update.Reader.Blind {
		t.Fatalf("Risky was not saved independently of terminal blind mode: %v", err)
	}
	if _, err := r.SetNoteVisibility("another.review.yaml", "hide"); err == nil {
		t.Fatal("the preference changed for the wrong review")
	}
	r = &Reviews{path: path}
	view, err = r.SelectWalkthrough(path, fileWalk)
	if err != nil {
		t.Fatal(err)
	}
	route := walkStop(t, view, "file:route.txt")
	retired := walkStop(t, view, "file:retired.txt")
	if view.Reader.NoteVisibility != "risky" || !route.Notes[0].HighRisk || retired.Notes[0].HighRisk {
		t.Fatalf("file grouping lost the saved mode or authored risk: %+v, %+v", route.Notes, retired.Notes)
	}
	if _, err := r.SetNoteVisibility(path, "hide"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reveal("another.review.yaml", "file:retired.txt"); err == nil {
		t.Fatal("revealed a note in the wrong review")
	}
	if _, err := r.Reveal(path, "overview"); err == nil {
		t.Fatal("revealed a non-code stop")
	}
	update, err := r.Reveal(path, "file:route.txt")
	if err != nil || update.Reader.NoteVisibility != "hide" || !slices.Equal(update.Reader.Revealed, []string{"file:route.txt"}) {
		t.Fatalf("reveal did not stay on one stop: %+v, %v", update, err)
	}
}

func TestWalkthroughSwitchReusesAndRefreshesSnapshot(t *testing.T) {
	path, repo := prWalkFixture(t)
	writeWalkReview(t, path, prWalkGrouped)
	r := &Reviews{path: path}
	original, err := r.Current()
	if err != nil {
		t.Fatal(err)
	}
	switchTo := func(mode walkMode) *View {
		t.Helper()
		view, err := r.SelectWalkthrough(path, mode)
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	fileDoc := r.walk.alternate
	switchTo(fileWalk)
	if r.loaded != fileDoc {
		t.Fatal("switch rebuilt an unchanged comparison")
	}
	guided := switchTo(agentWalk)
	if &guided.Stations[0] != &original.Stations[0] || guided.Version <= original.Version {
		t.Fatal("switch did not reuse the rendered view with a fresh version")
	}

	writeWalkReview(t, path, prWalkGrouped+prWalkLateNote)
	file := switchTo(fileWalk)
	if got := walkStop(t, file, "file:retired.txt").Notes; len(got) != 1 || got[0].AuthoredRef != "shipping:1" {
		t.Fatalf("switch missed the updated review or its authored reference: %+v", got)
	}
	set := coverage.Set{Executable: map[string][]int{"route.txt": {1}}}
	if err := set.Save(review.CoveragePath(path)); err != nil {
		t.Fatal(err)
	}
	if guided = switchTo(agentWalk); guided.Coverage == nil || walkStop(t, guided, "shipping").Cov == nil {
		t.Fatal("switch missed the new coverage")
	}
	if file = switchTo(fileWalk); file.Coverage == nil || walkStop(t, file, "file:route.txt").Cov == nil {
		t.Fatal("reused file view lost coverage")
	}
	const preparation = "snapshot-preparation"
	if err := updateReviewResume(path, func(saved *reviewResume) bool {
		saved.WalkPreparation = preparation
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if pending, err := r.Current(); err != nil || pending.Walkthrough.Ready {
		t.Fatalf("could not load a pending snapshot: %v", err)
	}
	if err := markWalkthroughReady([]string{"--review", path, "--preparation", preparation}); err != nil {
		t.Fatal(err)
	}
	if guided = switchTo(agentWalk); !guided.Walkthrough.Ready {
		t.Fatal("switch missed the walkthrough publication")
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new route\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file = switchTo(fileWalk)
	if stop := walkStop(t, file, "file:new.txt"); stop.Parts[0].File != "new.txt" {
		t.Fatalf("switch missed a new file: %+v", stop.Parts)
	}
}

func TestWalkthroughSwitchRefreshesStagedContents(t *testing.T) {
	path, repo := prWalkFixture(t)
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", "route.txt")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "Base")
	sha := git("rev-parse", "HEAD")
	yaml := strings.Replace(prWalkHeader, "base_dir: base", "base: "+sha+"\nstaged: true", 1)
	yaml += `stations:
  - id: shipping
    title: Select the new route
    what: Change the route selector.
    parts:
      - file: route.txt
        hunks: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	stage := func(line string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, "route.txt"), []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "route.txt")
	}
	stage("route by start")
	r := &Reviews{path: path}
	if _, err := r.Current(); err != nil {
		t.Fatal(err)
	}
	alternate := r.walk.alternate
	if _, err := r.SelectWalkthrough(path, fileWalk); err != nil || r.loaded != alternate {
		t.Fatalf("unchanged index was not reused: %v", err)
	}
	stage("route by final")
	view, err := r.SelectWalkthrough(path, agentWalk)
	if err != nil {
		t.Fatal(err)
	}
	row := walkStop(t, view, "shipping").Parts[0].Rows[0]
	if len(row.Spans) != 1 || row.Spans[0].Text != "route by final" {
		t.Fatalf("switch used old index content: %+v", row)
	}
}

func (r *Reviews) gotWalkPosition(t *testing.T, path string, want ReadingPosition) {
	t.Helper()
	if pos := r.ReadingPosition(path); !reflect.DeepEqual(pos, &want) {
		t.Fatalf("reading position = %+v, want %+v", pos, want)
	}
}

func walkStop(t *testing.T, view *View, id string) StationView {
	t.Helper()
	for _, stop := range view.Stations {
		if stop.ID == id {
			return stop
		}
	}
	t.Fatalf("missing stop %s in %+v", id, view.Stations)
	return StationView{}
}

func prWalkFixture(t *testing.T) (path, repo string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	for name, body := range map[string]string{
		"base/route.txt": "route by name\n", "base/retired.txt": "legacy route\n", "head/route.txt": "route by priority\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path = filepath.Join(dir, "pr.review.yaml")
	writeWalkReview(t, path, prWalkGenerated)
	return path, filepath.Join(dir, "head")
}

func writeWalkReview(t *testing.T, path, stations string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(prWalkHeader+stations), 0o600); err != nil {
		t.Fatal(err)
	}
}

const prWalkHeader = `version: 1
repo: head
base_dir: base
title: "PR #23: Prioritize shipments"
`

const prWalkGenerated = `stations:
  - id: retired
    title: retired.txt
    parts:
      - file: retired.txt
        side: base
        hunks: true
  - id: route
    title: route.txt
    parts:
      - file: route.txt
        hunks: true
`

const prWalkGrouped = `stations:
  - id: shipping
    title: Use the priority route
    what: The priority route replaces the retired route.
    parts:
      - file: route.txt
        hunks: true
      - file: retired.txt
        side: base
        hunks: true
      - file: route.txt
        side: base
        hunks: true
`

const prWalkLateNote = `    notes:
      - at: legacy route
        file: retired.txt
        kind: info
        text: The old route is gone.
      - at: route by name
        file: route.txt
        kind: info
        text: The old selector is gone. See shipping:1.
`
