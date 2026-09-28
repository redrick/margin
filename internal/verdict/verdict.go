// Package verdict turns the reader's stop verdicts, comments and calls into one recommendation for
// the whole change, with the reasons behind it.
package verdict

import (
	"fmt"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/state"
)

// The reader's verdict on one stop.
const (
	Good    = "good"
	Changes = "changes"
	Unsure  = "unsure"
)

// The reader's answer to whether the change makes sense as done, given before reading any code.
const (
	OrientYes    = "yes"
	OrientNo     = "no"
	OrientUnsure = "unsure"
)

// The recommendation for the whole change.
const (
	Approve        = "approve"
	RequestChanges = "request changes"
	NotFinished    = "not finished"
)

type Outcome struct {
	Decision string
	// Blockers are why the change cannot be approved as it is; Left is what the reader has not
	// settled yet; Notes add what goes along with an approval.
	Blockers, Left, Notes []string
}

// Recommend weighs the reader's own marks only: the agent's notes count once the reader flagged
// them, never on their own.
func Recommend(d *doc.Doc, st *state.State) Outcome {
	var o Outcome
	var changes, unsure, open []string
	for _, s := range d.Stations {
		if s.Kind != doc.Code {
			continue
		}
		switch st.Verdicts[s.ID] {
		case Changes:
			changes = append(changes, s.ID)
		case Unsure:
			unsure = append(unsure, s.ID)
		case Good:
		default:
			open = append(open, s.ID)
		}
	}
	resolved := d.AnsweredQuestions()
	blocking, other := 0, 0
	for _, q := range st.Questions {
		if !q.IsComment() || resolved[q.ID] {
			continue
		}
		if _, b := state.CommentLabel(q.Text); b {
			blocking++
		} else {
			other++
		}
	}
	flagged, undecided, calls := 0, 0, 0
	for _, s := range d.Stations {
		for _, n := range s.Notes {
			switch {
			case st.Dismissed[n.Key]:
			case n.Level() == doc.KindDecide && !st.Reviewed[n.Key]:
				calls++
			case n.Level() == doc.KindIssue && !n.Unbacked() && st.Flagged[n.Key]:
				flagged++
			case n.Level() == doc.KindIssue && !n.Unbacked():
				undecided++
			}
		}
	}

	if st.Orient == OrientNo {
		why := "you said the change does not make sense as it is done"
		if st.Design != "" {
			why += ": " + st.Design
		}
		o.Blockers = append(o.Blockers, why)
	}
	if len(changes) > 0 {
		o.Blockers = append(o.Blockers, fmt.Sprintf("you marked %s as needing changes", list(changes)))
	}
	if blocking > 0 {
		o.Blockers = append(o.Blockers, count(blocking, "blocking comment is", "blocking comments are")+" not resolved")
	}
	if flagged > 0 {
		o.Blockers = append(o.Blockers, count(flagged, "problem the agent found is", "problems the agent found are")+" flagged by you")
	}
	if len(open) > 0 {
		o.Left = append(o.Left, fmt.Sprintf("no verdict yet on %s", list(open)))
	}
	if len(unsure) > 0 {
		o.Left = append(o.Left, fmt.Sprintf("you were unsure about %s: ask the agent, or read it again", list(unsure)))
	}
	if calls > 0 {
		o.Left = append(o.Left, count(calls, "call is", "calls are")+" still yours to make")
	}
	if undecided > 0 {
		o.Left = append(o.Left, count(undecided, "problem the agent found is", "problems the agent found are")+" neither flagged (?) nor dismissed (x)")
	}
	if other > 0 {
		o.Notes = append(o.Notes, count(other, "non-blocking comment goes", "non-blocking comments go")+" with it")
	}
	switch {
	case len(o.Blockers) > 0:
		o.Decision = RequestChanges
	case len(o.Left) > 0:
		o.Decision = NotFinished
	default:
		o.Decision = Approve
	}
	return o
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func list(ids []string) string {
	switch len(ids) {
	case 1:
		return ids[0]
	case 2:
		return ids[0] + " and " + ids[1]
	}
	if len(ids) > 4 {
		return fmt.Sprintf("%d stops", len(ids))
	}
	s := ids[0]
	for _, id := range ids[1 : len(ids)-1] {
		s += ", " + id
	}
	return s + " and " + ids[len(ids)-1]
}
