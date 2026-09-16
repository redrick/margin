package main

import (
	"testing"

	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/testutil"
)

func TestAnswerWritesNote(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	path := testutil.Example(t)
	st, err := state.Load(review.StatePath(path))
	if err != nil {
		t.Fatal(err)
	}
	st.Questions = []state.Question{
		{ID: "q1", Station: "reserve", File: "inventory/stock.go", Line: 36, Needle: "defer s.mu.Unlock()", Text: "why defer?"},
		{ID: "q2", Station: "store", File: "inventory/stock.go", Line: 22, Needle: "}", Text: "which brace?"},
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	if code := run([]string{"answer", "q1", "because", "panics", "--review", path}); code != 0 {
		t.Fatalf("answer q1 exit %d", code)
	}
	if code := run([]string{"answer", "--review", path, "q1", "again"}); code == 0 {
		t.Fatal("answering twice should fail")
	}
	if code := run([]string{"answer", "--review", path, "q2", "the NewStore one"}); code != 0 {
		t.Fatalf("answer q2 exit %d", code)
	}

	r, err := review.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, reserve := r.Station("reserve")
	n := reserve.Notes[len(reserve.Notes)-1]
	if n.QID != "q1" || n.Text != "because panics" || n.Q != "why defer?" || n.Nth != 0 {
		t.Fatalf("q1 note = %+v", n)
	}
	_, store := r.Station("store")
	n = store.Notes[len(store.Notes)-1]
	if n.QID != "q2" || n.Nth != 3 {
		t.Fatalf("q2 note should pick the brace on line 22, the third match in the station, got %+v", n)
	}

	if code := run([]string{"lint", path}); code != 0 {
		t.Fatal("review no longer lints after answers")
	}
}
