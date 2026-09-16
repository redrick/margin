package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/redrick/margin/internal/control"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/tmuxx"
	"github.com/redrick/margin/internal/tui"
	"github.com/redrick/margin/internal/watch"
)

const version = "0.1.0-dev"

const usageText = `margin — review git changes with an agent beside you

  margin              review uncommitted changes
  margin <branch>     review a branch against the main branch
  margin <commit>     review one commit

  The review (code and comments) opens on top, a new claude (or $MARGIN_AGENT) reviewer below.
  Inside gw it gets its own sub-window; in plain tmux it splits the current pane; outside tmux
  it takes over the terminal.
  --base REF   compare against REF    --fresh     start the review over
  --no-agent   only open the viewer

  Reviews are kept in $MARGIN_DIR, else in the vault of Claude Code's obsidian-memory skill,
  else in ~/.local/state/margin.

keys: ] [ note · } { file · a ask about this line · space reviewed · tab list · q quit

commands for the agent:
  margin note <file>:<line> "text"     add a note
  margin answer <qid> "text"           answer a question asked in the viewer
  margin goto <station>[:<note>] | <file>:<line>
  margin where | reload | questions | lint <review.yaml>
  margin open | split <review.yaml>    open a review file directly
  margin brief [--review FILE]         the review task for the agent
  margin agent-help                    instructions for the agent
`

var commands = map[string]bool{
	"review": true, "open": true, "split": true, "lint": true, "goto": true, "where": true, "reload": true,
	"questions": true, "answer": true, "note": true, "brief": true, "session": true, "agent-help": true, "version": true, "help": true,
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	cmd, rest := "review", args
	if len(args) > 0 && commands[args[0]] {
		cmd, rest = args[0], args[1:]
	}
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		cmd = "help"
	}
	var err error
	switch cmd {
	case "review":
		err = cmdReview(rest)
	case "open":
		err = cmdOpen(rest)
	case "split":
		err = cmdSplit(rest)
	case "lint":
		return cmdLint(rest)
	case "goto":
		err = cmdControl("goto", rest, 1)
	case "where":
		err = cmdControl("where", rest, 0)
	case "reload":
		err = cmdControl("reload", rest, 0)
	case "questions":
		err = cmdQuestions(rest)
	case "answer":
		err = cmdAnswer(rest)
	case "note":
		err = cmdNote(rest)
	case "brief":
		err = cmdBrief(rest)
	case "session":
		err = cmdSession(rest)
	case "agent-help":
		fmt.Print(agentHelp)
	case "version":
		fmt.Println("margin", version)
	case "help":
		fmt.Print(usageText)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "margin:", err)
		return 1
	}
	return 0
}

// parseArgs lets flags appear anywhere, which is how people and agents actually type commands.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	if err := fs.Parse(flags); err != nil {
		return nil, fmt.Errorf("%s: %w", fs.Name(), err)
	}
	return pos, nil
}

func reviewArg(pos []string, cmd string) (string, error) {
	if len(pos) != 1 {
		return "", fmt.Errorf("usage: margin %s <review.yaml>", cmd)
	}
	abs, err := filepath.Abs(pos[0])
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", err
	}
	return abs, nil
}

func cmdOpen(args []string) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	agent := fs.String("agent-pane", "", "")
	noSubmit := fs.Bool("no-submit", false, "")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	path, err := reviewArg(pos, "open")
	if err != nil {
		return err
	}
	sock := control.SocketPath(path)
	if control.Alive(sock) {
		return fmt.Errorf("%s is already open (socket %s)", pos[0], sock)
	}

	m := tui.New(tui.Options{ReviewPath: path, AgentPane: *agent, Submit: !*noSubmit})
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if w, err := watch.New(func() { p.Send(tui.ChangedMsg{}) }); err == nil {
		defer w.Close()
		w.Set([]string{path})
		m.SetWatcher(w)
	}
	srv, err := control.Serve(sock, tui.Handler(p))
	if err != nil {
		return err
	}
	defer srv.Close()
	_, err = p.Run()
	return err
}

func cmdSplit(args []string) error {
	fs := flag.NewFlagSet("split", flag.ContinueOnError)
	size := fs.Int("size", 65, "")
	noSubmit := fs.Bool("no-submit", false, "")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	path, err := reviewArg(pos, "split")
	if err != nil {
		return err
	}
	if !tmuxx.InTmux() {
		return fmt.Errorf("not inside tmux; run `margin open %s` in another terminal instead", pos[0])
	}
	if _, err := review.Load(path); err != nil {
		return err
	}
	sock := control.SocketPath(path)
	if control.Alive(sock) {
		fmt.Println("already open; socket", sock)
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	agent := tmuxx.CurrentPane()
	argv := []string{exe, "open"}
	if agent != "" {
		argv = append(argv, "--agent-pane", agent)
	}
	if *noSubmit {
		argv = append(argv, "--no-submit")
	}
	pane, err := tmuxx.Split(agent, append(argv, path), min(max(*size, 20), 90))
	if err != nil {
		return err
	}
	for i := 0; i < 50 && !control.Alive(sock); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !control.Alive(sock) {
		return fmt.Errorf("viewer in pane %s did not start; check that pane for the error", pane)
	}
	fmt.Printf("margin open in pane %s; questions go to pane %s\n", pane, agent)
	return nil
}

func cmdLint(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: margin lint <review.yaml>...")
		return 2
	}
	code := 0
	for _, path := range args {
		r, err := review.Load(path)
		if err != nil {
			fmt.Println(err)
			code = 1
			continue
		}
		files, err := source.Open(r)
		if err != nil {
			fmt.Printf("%s: %v\n", path, err)
			code = 1
			continue
		}
		d := doc.Build(r, files)
		files.Close()
		for _, p := range d.Problems {
			fmt.Printf("%s: %s: %s\n", path, p.Station, p.Msg)
		}
		if len(d.Problems) > 0 {
			fmt.Printf("%s: %d problems · %s\n", path, len(d.Problems), doc.Summary(d))
			code = 1
		} else {
			fmt.Printf("%s: ok · %s\n", path, doc.Summary(d))
		}
	}
	return code
}

func locate(reviewFlag string) (sock, reviewPath string, err error) {
	if reviewFlag == "" {
		reviewFlag = os.Getenv("MARGIN_REVIEW")
	}
	if reviewFlag != "" {
		abs, err := filepath.Abs(reviewFlag)
		if err != nil {
			return "", "", err
		}
		sock = control.SocketPath(abs)
		if !control.Alive(sock) {
			sock = ""
		}
		return sock, abs, nil
	}
	live := control.Live()
	var paths []string
	for _, s := range live {
		resp, err := control.Call(s, control.Request{Cmd: "review"})
		if err != nil {
			continue
		}
		var info struct {
			Review string `json:"review"`
		}
		if json.Unmarshal(resp.Data, &info) == nil {
			sock, reviewPath = s, info.Review
			paths = append(paths, info.Review)
		}
	}
	switch len(paths) {
	case 0:
		return "", "", errors.New("no running margin viewer; start one with `margin` or pass --review")
	case 1:
		return sock, reviewPath, nil
	}
	return "", "", fmt.Errorf("%d viewers are running, pass --review:\n  %s", len(paths), strings.Join(paths, "\n  "))
}

func cmdControl(cmd string, args []string, nargs int) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	reviewFlag := fs.String("review", "", "")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != nargs {
		return fmt.Errorf("usage: margin %s", strings.TrimSpace(cmd+" "+strings.Repeat("<target> ", nargs)))
	}
	sock, path, err := locate(*reviewFlag)
	if err != nil {
		return err
	}
	if sock == "" {
		return fmt.Errorf("no viewer is running for %s", path)
	}
	req := control.Request{Cmd: cmd}
	if nargs > 0 {
		req.Arg = pos[0]
	}
	resp, err := control.Call(sock, req)
	if err != nil {
		return err
	}
	return printJSON(resp.Data)
}

func printJSON(data json.RawMessage) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

func cmdQuestions(args []string) error {
	fs := flag.NewFlagSet("questions", flag.ContinueOnError)
	reviewFlag := fs.String("review", "", "")
	all := fs.Bool("all", false, "")
	asJSON := fs.Bool("json", false, "")
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
	st, err := state.Load(review.StatePath(path))
	if err != nil {
		return err
	}
	qs := tui.Questions(r, st, *all)
	if *asJSON {
		data, err := json.Marshal(qs)
		if err != nil {
			return err
		}
		return printJSON(data)
	}
	if len(qs) == 0 {
		fmt.Println("no open questions")
		return nil
	}
	for _, q := range qs {
		status := "open"
		if q.Answered {
			status = "answered"
		}
		fmt.Printf("%s  %s  %s:%d  %s\n    line: %s\n    %s\n", q.ID, q.Station, q.File, q.Line, status, q.Needle, q.Text)
	}
	return nil
}

func cmdAnswer(args []string) error {
	fs := flag.NewFlagSet("answer", flag.ContinueOnError)
	reviewFlag := fs.String("review", "", "")
	noGoto := fs.Bool("no-goto", false, "")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return errors.New(`usage: margin answer <qid> "<answer>"   (use - to read the answer from stdin)`)
	}
	qid := pos[0]
	answer, err := readText(pos[1:])
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
	st, err := state.Load(review.StatePath(path))
	if err != nil {
		return err
	}
	q, ok := st.Question(qid)
	if !ok {
		return fmt.Errorf("no question %s in %s", qid, review.StatePath(path))
	}
	for _, s := range r.Stations {
		for i, n := range s.Notes {
			if n.QID == qid {
				return fmt.Errorf("%s is already answered in station %s note %d; edit that note instead", qid, s.ID, i+1)
			}
		}
	}

	files, err := source.Open(r)
	if err != nil {
		return err
	}
	d := doc.Build(r, files)
	files.Close()
	_, station := d.Station(q.Station)
	if station == nil {
		return fmt.Errorf("station %s no longer exists", q.Station)
	}
	hits := station.Hits(q.File, q.Needle)
	if len(hits) == 0 {
		return fmt.Errorf("line %q (%s:%d) is no longer in station %s; add the note with margin note", q.Needle, q.File, q.Line, q.Station)
	}

	note := review.Note{At: q.Needle, Nth: nthFor(hits, q.Line-1), File: q.File, Q: q.Text, QID: q.ID, Text: answer}
	idx, err := review.AppendNote(path, q.Station, note)
	if err != nil {
		return err
	}
	fmt.Printf("%s answered: station %s note %d\n", q.ID, q.Station, idx+1)
	return notifyViewer(sock, !*noGoto, q.Station, idx)
}
