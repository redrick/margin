package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/redrick/margin/internal/control"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/target"
	"github.com/redrick/margin/internal/tmuxx"
)

func cmdReview(args []string) error {
	fs := flag.NewFlagSet("margin", flag.ContinueOnError)
	baseFlag := fs.String("base", "", "")
	fresh := fs.Bool("fresh", false, "")
	noAgent := fs.Bool("no-agent", false, "")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return errors.New("usage: margin [branch | commit] [--base REF] [--fresh] [--no-agent]")
	}
	arg := ""
	if len(pos) == 1 {
		arg = pos[0]
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	t, err := target.Resolve(wd, arg, *baseFlag)
	if err != nil {
		return err
	}
	loc := target.Locate(t)
	path := loc.Path
	created, added, err := target.Ensure(loc, t, *fresh)
	if err != nil {
		return err
	}
	switch {
	case created:
		fmt.Printf("%s · %d changed files\n", t.Title, len(t.Changes))
	case added > 0:
		fmt.Printf("%s · resuming, %d newly changed files added\n", t.Title, added)
	default:
		fmt.Printf("%s · resuming the existing review\n", t.Title)
	}
	fmt.Println("review file:", path)

	if control.Alive(control.SocketPath(path)) {
		fmt.Println("this review is already open in another pane")
		return nil
	}
	if *noAgent {
		return cmdOpen([]string{path})
	}

	agent := agentCommand()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	sessionArgs := []string{"session", "--review", path}
	if created {
		sessionArgs = append(sessionArgs, "--new")
	}
	inAgent := os.Getenv("CLAUDECODE") == "1"
	switch {
	case !tmuxx.InTmux():
		return cmdSession(sessionArgs[1:])
	case tmuxx.SessionName() == "gw":
		if err := requireAgent(agent); err != nil {
			return err
		}
		return openInGW(exe, sessionArgs, path)
	case !inAgent:
		if err := requireAgent(agent); err != nil {
			return err
		}
	}
	brief := "margin brief --review " + tmuxx.Quote(path)
	if created {
		brief += " --new"
	}
	if inAgent {
		fmt.Println("the viewer opens beside this pane; review instructions follow as a message")
	}
	return tmuxx.Launch(tmuxx.Layout{
		Dir:      t.Repo,
		Name:     "margin " + t.Label(),
		AgentCmd: agent + " " + tmuxx.Quote(agentPrompt(t.Title, t.Kicker, t.Head != "", loc, created)),
		InAgent:  inAgent,
		Kickoff:  "Start the margin code review: run `" + brief + "` and follow its instructions.",
		Viewer: func(pane string) []string {
			return []string{exe, "open", "--agent-pane", pane, path}
		},
		ViewerPercent: 60,
	})
}

func cmdBrief(args []string) error {
	fs := flag.NewFlagSet("brief", flag.ContinueOnError)
	reviewFlag := fs.String("review", "", "")
	isNew := fs.Bool("new", false, "")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	_, path, err := locate(*reviewFlag)
	if err != nil {
		return err
	}
	r, err := review.Load(path)
	if err != nil {
		return err
	}
	fmt.Println(agentPrompt(r.Title, r.Kicker, r.Head != "", target.LocationOf(path), *isNew))
	return nil
}

func agentPrompt(title, kicker string, readOnly bool, loc target.Location, created bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Code review with margin. The viewer in the pane beside you shows: %s (%s).\n", title, kicker)
	fmt.Fprintf(&b, "Review file: %s\n", loc.Path)
	if loc.Vault != "" {
		fmt.Fprintf(&b, "It lives in your Obsidian memory vault under %s/topics/%s; list it in that project's index as your memory rules say.\n", loc.Project, loc.Topic)
	}
	b.WriteString("\nRun `margin agent-help` first and follow it.\n")
	b.WriteString("Every note is read by a person who did not write this code and is reading it for the first time. Write for that person: " +
		"plain, simple language in full sentences, never compressed or telegraphic, even if you are otherwise asked to be terse. " +
		"Comment a bit more than feels necessary: what the code does, why it changed, and what to watch out for. " +
		"Where it helps, add cues for understanding: what calls this and when, what the old behaviour was, a small example of input and result, " +
		"or which other note to read first.\n")
	if created {
		b.WriteString("Then shape the tour first (agent-help step 0: riskiest stop first, risk, concern and tests per stop, summary of what and why). " +
			"After that annotate every change with `margin note`, giving each note a --kind; a problem (--kind issue) needs --evidence or it is shown as a question. " +
			"Mark parts you checked and found fine with a short --kind ok note, so parts without notes honestly mean not examined.\n")
	} else {
		b.WriteString("This review may already have notes. Annotate the stations that have none yet and update notes whose code changed.\n")
	}
	b.WriteString("When done, give me a two-line verdict and wait. My questions arrive as \"[margin qN] ...\": answer them, then record the answer with `margin answer`. " +
		"If I ask for a code change, make it and update the affected notes.\n")
	if readOnly {
		b.WriteString("The reviewed code is not checked out, so do not edit files unless I ask you to check it out first.\n")
	}
	b.WriteString("Do not commit, push or run any other git write command unless I ask.")
	return b.String()
}

func cmdNote(args []string) error {
	fs := flag.NewFlagSet("note", flag.ContinueOnError)
	reviewFlag := fs.String("review", "", "")
	stationFlag := fs.String("station", "", "")
	jump := fs.Bool("goto", false, "")
	kind := fs.String("kind", "", "")
	evidence := fs.String("evidence", "", "")
	confidence := fs.String("confidence", "", "")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return errors.New(`usage: margin note <file>:<line> "<text>"   (use - to read the text from stdin)`)
	}
	body, err := readText(pos[1:])
	if err != nil {
		return err
	}
	sock, path, err := locate(*reviewFlag)
	if err != nil {
		return err
	}
	r, err := review.Load(path)
	if err != nil {
		return err
	}
	file, line, err := splitLoc(pos[0], r.RepoDir())
	if err != nil {
		return err
	}
	files, err := source.Open(r)
	if err != nil {
		return err
	}
	d := doc.Build(r, files)
	files.Close()

	st, part := findLine(d, *stationFlag, file, line-1)
	if st == nil {
		return fmt.Errorf("%s:%d is not inside any change shown by the review; pick a changed line or add a part for it", file, line)
	}
	needle, at := needleAt(part, line-1)
	if needle == "" {
		return fmt.Errorf("%s:%d: no non-blank line nearby to anchor on", file, line)
	}
	note := review.Note{At: needle, Nth: nthFor(st.Hits(file, needle), at), File: file, Text: body,
		Kind: *kind, Evidence: *evidence, Confidence: *confidence}
	idx, err := review.AppendNote(path, st.ID, note)
	if err != nil {
		return err
	}
	fmt.Printf("note %s:%d on %s:%d\n", st.ID, idx+1, file, at+1)
	return notifyViewer(sock, *jump, st.ID, idx)
}

func findLine(d *doc.Doc, stationID, file string, line int) (*doc.Station, *doc.Part) {
	for _, base := range []bool{false, true} {
		for _, s := range d.Stations {
			if s.Kind != doc.Code || (stationID != "" && s.ID != stationID) {
				continue
			}
			for _, p := range s.Parts {
				if p.Err == nil && p.Base == base && p.Spec.File == file && p.Range.Contains(line) {
					return s, p
				}
			}
		}
	}
	return nil, nil
}

func splitLoc(loc, repo string) (string, int, error) {
	i := strings.LastIndex(loc, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("want <file>:<line>, got %q", loc)
	}
	line, err := strconv.Atoi(loc[i+1:])
	if err != nil || line < 1 {
		return "", 0, fmt.Errorf("want <file>:<line>, got %q", loc)
	}
	file := loc[:i]
	if filepath.IsAbs(file) {
		if rel, err := filepath.Rel(repo, file); err == nil {
			file = rel
		}
	}
	return filepath.ToSlash(filepath.Clean(file)), line, nil
}

func needleAt(p *doc.Part, line int) (string, int) {
	for l := line; l >= p.Range.Start && l >= 0; l-- {
		if t := strings.TrimSpace(p.Lines[l]); t != "" {
			return t, l
		}
	}
	for l := line + 1; l <= p.Range.End && l < len(p.Lines); l++ {
		if t := strings.TrimSpace(p.Lines[l]); t != "" {
			return t, l
		}
	}
	return "", line
}

func nthFor(hits []doc.Hit, line int) int {
	if len(hits) < 2 {
		return 0
	}
	best := 0
	for i, h := range hits {
		if dist(h.Line, line) < dist(hits[best].Line, line) {
			best = i
		}
	}
	return best + 1
}

func readText(parts []string) (string, error) {
	s := strings.Join(parts, " ")
	if s == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		s = string(data)
	}
	if s = strings.TrimSpace(s); s == "" {
		return "", errors.New("empty text")
	}
	return s, nil
}

func notifyViewer(sock string, jump bool, station string, idx int) error {
	if sock == "" {
		return nil
	}
	if _, err := control.Call(sock, control.Request{Cmd: "reload"}); err != nil {
		return fmt.Errorf("note written, but the viewer failed to reload: %w", err)
	}
	if !jump {
		return nil
	}
	_, err := control.Call(sock, control.Request{Cmd: "goto", Arg: fmt.Sprintf("%s:%d", station, idx+1)})
	return err
}

func dist(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}
