package verdict

import (
	"strings"
	"testing"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/testutil"
)

func TestRecommend(t *testing.T) {
	r, err := review.Load(testutil.Example(t))
	if err != nil {
		t.Fatal(err)
	}
	files, err := source.Open(r)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	d := doc.Build(r, files)
	st, _ := state.Load(t.TempDir() + "/s.yaml")

	if o := Recommend(d, st); o.Decision != NotFinished || !strings.Contains(strings.Join(o.Left, "|"), "no verdict yet on 5 stops") {
		t.Fatalf("nothing read yet: %+v", o)
	}
	for _, s := range d.Stations {
		if s.Kind == doc.Code {
			st.Verdicts[s.ID] = Good
		}
		for _, n := range s.Notes {
			switch {
			case n.Level() == doc.KindDecide:
				st.Reviewed[n.Key] = true
			case n.Level() == doc.KindIssue:
				st.Dismissed[n.Key] = true
			}
		}
	}
	st.Questions = append(st.Questions, state.Question{ID: "c1", Kind: state.KindComment, Text: "nitpick: spacing"})
	if o := Recommend(d, st); o.Decision != Approve || len(o.Notes) != 1 {
		t.Fatalf("all good with a nitpick: %+v", o)
	}
	st.Questions = append(st.Questions, state.Question{ID: "c2", Kind: state.KindComment, Text: "issue (blocking): races"})
	if o := Recommend(d, st); o.Decision != RequestChanges || o.Blockers[0] != "1 blocking comment is not resolved" {
		t.Fatalf("a blocking comment: %+v", o)
	}
}
