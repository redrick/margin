package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/verdict"
)

// FixItem is one thing the reader wants changed and has not sent to the agent yet: a problem note
// they agreed with (?), or a stop they gave the verdict "needs changes".
type FixItem struct {
	Key     string
	Station *doc.Station
	Note    *doc.Note
}

// stopFixKey is the fix key of a stop with the verdict "needs changes".
func stopFixKey(id string) string { return "stop:" + id }

// ToFix lists what the reader wants fixed and has not sent yet, in review order.
func ToFix(d *doc.Doc, st *state.State) []FixItem {
	if d == nil || st == nil {
		return nil
	}
	sent := map[string]bool{}
	for _, q := range st.Questions {
		if q.Fix != "" {
			sent[q.Fix] = true
		}
	}
	var out []FixItem
	for _, s := range d.Stations {
		if s.Kind != doc.Code {
			continue
		}
		if st.Verdicts[s.ID] == verdict.Changes && !sent[stopFixKey(s.ID)] {
			out = append(out, FixItem{Key: stopFixKey(s.ID), Station: s})
		}
		for _, n := range s.Notes {
			if n.Level() == doc.KindIssue && !n.Unbacked() && st.Flagged[n.Key] && !st.Dismissed[n.Key] && !sent[n.Key] {
				out = append(out, FixItem{Key: n.Key, Station: s, Note: n})
			}
		}
	}
	return out
}

// AddFixes records each item of ToFix as a sent comment (cN) that asks for the fix, so the agent
// resolves it like any comment, and returns them for FixMessage.
func AddFixes(d *doc.Doc, st *state.State) []state.Question {
	var out []state.Question
	for _, it := range ToFix(d, st) {
		q := state.Question{ID: st.NextCommentID(), Kind: state.KindComment, Fix: it.Key, Station: it.Station.ID,
			Asked: time.Now().UTC().Truncate(time.Second)}
		if n := it.Note; n != nil {
			q.File, q.Needle, q.Text = n.File, strings.TrimSpace(n.At), oneLine(plain(n.Text))
			if n.Part >= 0 && n.Part < len(it.Station.Parts) {
				p := it.Station.Parts[n.Part]
				l := n.Line
				needle := strings.TrimSpace(p.Lines[l])
				for needle == "" && l > p.Range.Start {
					l--
					needle = strings.TrimSpace(p.Lines[l])
				}
				q.File, q.Side, q.Line, q.Needle = p.Spec.File, p.Spec.Side, n.Line+1, needle
			}
		} else {
			q.Text = stopFixText(it.Station, st)
			// The request sits on the stop's first non-blank line, so margin resolve finds it.
			if len(it.Station.Parts) > 0 {
				p := it.Station.Parts[0]
				for l := p.Range.Start; l <= p.Range.End && l < len(p.Lines); l++ {
					if needle := strings.TrimSpace(p.Lines[l]); needle != "" {
						q.File, q.Side, q.Line, q.Needle = p.Spec.File, p.Spec.Side, l+1, needle
						break
					}
				}
			}
		}
		st.Questions = append(st.Questions, q)
		out = append(out, q)
	}
	return out
}

// stopFixText asks for the changes to a whole stop, with the reader's own remarks on it as the brief.
func stopFixText(s *doc.Station, st *state.State) string {
	title := s.Title
	if title == "" {
		title = s.ID
	}
	var said []string
	for _, q := range st.Questions {
		if q.IsComment() && q.Fix == "" && q.Station == s.ID {
			said = append(said, fmt.Sprintf("%s (%s:%d) %q", q.ID, q.File, q.Line, oneLine(q.Text)))
		}
	}
	text := fmt.Sprintf("The reader's verdict on stop %q is: needs changes.", title)
	if len(said) == 0 {
		return text + " They wrote no comment on it: ask in chat what they want changed before you touch its code."
	}
	return text + " What they said on it: " + strings.Join(said, "; ") + ". Make the changes those ask for."
}

// DropFixes takes back fix requests that could not be sent, so they count as not sent.
func DropFixes(st *state.State, ids []string) {
	kept := st.Questions[:0]
	for _, q := range st.Questions {
		drop := false
		for _, id := range ids {
			if q.ID == id && q.Fix != "" {
				drop = true
			}
		}
		if !drop {
			kept = append(kept, q)
		}
	}
	st.Questions = kept
}

// FixMessage is the one line the agent receives for what the reader wants fixed.
func FixMessage(qs []state.Question, answers string) string {
	parts := make([]string, len(qs))
	for i, q := range qs {
		parts[i] = fmt.Sprintf("%s %s:%d (station %s, line `%s`): %s", q.ID, q.File, q.Line, q.Station, doc.Short(q.Needle, 60), q.Text)
	}
	return fmt.Sprintf("[margin fix] %d things the reader wants changed: problems you pointed out that they agree with, and stops they judged as needing changes. "+
		"Fix each one in the code, add changed: \"what and why\" to the affected notes and run the tests that cover it. "+
		"Then answer each in %s (create it if missing, append otherwise): a heading with the id and file:line, the request quoted, then what you changed. "+
		"Close it with margin resolve <id> \"Fixed: ...\". If one should not be fixed after all, leave its code alone and say why instead. %s",
		len(qs), answers, strings.Join(parts, " | "))
}
