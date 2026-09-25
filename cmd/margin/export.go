package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/state"
)

// remark is one thing the reader wants to say on a line: a comment they wrote, or a note they flagged.
type remark struct {
	File, Side string
	Line       int
	Body       string
}

// cmdExport prints what the reader has to say, ready to post by hand. margin itself never posts.
func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	reviewFlag := fs.String("review", "", "")
	format := fs.String("format", "md", "")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if *format != "md" && *format != "github" {
		return errors.New("usage: margin export [--format md|github] [--review FILE]")
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
	files, err := source.Open(r)
	if err != nil {
		return err
	}
	d := doc.Build(r, files)
	files.Close()

	remarks, calls := collect(d, st)
	if *format == "github" {
		return exportGitHub(remarks, calls)
	}
	fmt.Print(exportMarkdown(d, remarks, calls))
	return nil
}

func collect(d *doc.Doc, st *state.State) (remarks []remark, calls []string) {
	resolution := map[string]string{}
	for _, n := range d.CodeNotes() {
		if n.QID != "" {
			resolution[n.QID] = strings.TrimSpace(n.Text)
		}
	}
	for _, q := range st.Questions {
		if !q.IsComment() {
			continue
		}
		body := strings.TrimSpace(q.Text)
		if res := resolution[q.ID]; res != "" {
			body += "\n\n> " + strings.ReplaceAll(res, "\n", "\n> ")
		}
		remarks = append(remarks, remark{File: q.File, Side: q.Side, Line: q.Line, Body: body})
	}
	for _, s := range d.Stations {
		for _, n := range s.Notes {
			if n.Part < 0 || st.Dismissed[n.Key] {
				continue
			}
			p := s.Parts[n.Part]
			where := fmt.Sprintf("%s:%d", p.Spec.File, n.Line+1)
			if n.Level() == doc.KindDecide {
				box := "[ ]"
				if st.Reviewed[n.Key] {
					box = "[x]"
				}
				calls = append(calls, fmt.Sprintf("- %s %s (`%s`)", box, oneLine(n.Text), where))
			}
			if !st.Flagged[n.Key] {
				continue
			}
			side := ""
			if p.Base {
				side = review.SideBase
			}
			body := strings.TrimSpace(n.Text)
			if n.Evidence != "" {
				body += "\n\nEvidence: " + strings.TrimSpace(n.Evidence)
			}
			remarks = append(remarks, remark{File: p.Spec.File, Side: side, Line: n.Line + 1, Body: body})
		}
	}
	return remarks, calls
}

func exportMarkdown(d *doc.Doc, remarks []remark, calls []string) string {
	r := d.Review
	var b strings.Builder
	fmt.Fprintf(&b, "# Review: %s\n\n", r.Title)
	section := func(title, body string) {
		if body = strings.TrimSpace(body); body != "" {
			fmt.Fprintf(&b, "## %s\n\n%s\n\n", title, body)
		}
	}
	section("What the change does", r.Did)
	section("Compared with what was asked", r.Gap)
	if len(remarks) > 0 {
		b.WriteString("## Comments\n\n")
		for _, rm := range remarks {
			fmt.Fprintf(&b, "- `%s:%d`%s: %s\n", rm.File, rm.Line, sideLabel(rm.Side), indent(rm.Body))
		}
		b.WriteString("\n")
	}
	if len(calls) > 0 {
		b.WriteString("## Decisions\n\n" + strings.Join(calls, "\n") + "\n\n")
	}
	if len(remarks) == 0 && len(calls) == 0 {
		b.WriteString("Nothing to export yet: write comments with c, or flag notes with ?, in the viewer.\n")
	}
	return b.String()
}

// indent hangs the lines after the first under a list item, leaving blank lines blank.
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			lines[i] = "  " + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func sideLabel(side string) string {
	if side == review.SideBase {
		return " (old code)"
	}
	return ""
}

// exportGitHub prints the body of a pull request review for the GitHub API. The reader posts it,
// for example with gh api repos/OWNER/REPO/pulls/N/reviews --input FILE.
func exportGitHub(remarks []remark, calls []string) error {
	type comment struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Side string `json:"side"`
		Body string `json:"body"`
	}
	out := struct {
		Event    string    `json:"event"`
		Body     string    `json:"body"`
		Comments []comment `json:"comments"`
	}{Event: "COMMENT", Comments: []comment{}}
	if len(calls) > 0 {
		out.Body = "Decisions:\n\n" + strings.Join(calls, "\n")
	}
	for _, rm := range remarks {
		side := "RIGHT"
		if rm.Side == review.SideBase {
			side = "LEFT"
		}
		out.Comments = append(out.Comments, comment{Path: rm.File, Line: rm.Line, Side: side, Body: rm.Body})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	fmt.Fprintln(os.Stderr, "margin does not post this. To post it yourself: gh api repos/OWNER/REPO/pulls/N/reviews --input FILE")
	return nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
