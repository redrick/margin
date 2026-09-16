package doc_test

import (
	"testing"

	"github.com/redrick/margin/internal/diffmap"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/testutil"
)

func build(t *testing.T) *doc.Doc {
	t.Helper()
	r, err := review.Load(testutil.Example(t))
	if err != nil {
		t.Fatal(err)
	}
	files, err := source.Open(r)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	return doc.Build(r, files)
}

func TestExampleResolves(t *testing.T) {
	d := build(t)
	for _, p := range d.Problems {
		t.Errorf("%s: %s", p.Station, p.Msg)
	}
	if got := len(d.Stations); got != 8 {
		t.Fatalf("stations = %d, want overview + 5 + recap + tests", got)
	}
	if _, recap := d.Station("recap"); recap == nil || recap.Kind != doc.Recap {
		t.Fatal("a review with problems or questions gets a recap stop")
	}

	_, reserve := d.Station("reserve")
	part := reserve.Parts[0]
	if part.Range.Start != 29 || part.Range.End != 43 {
		t.Errorf("Reserve range = %+v, want doc comment through closing brace", part.Range)
	}
	if n := reserve.Notes[0]; n.Line != 31 {
		t.Errorf("note 1 on line %d, want 31", n.Line)
	}
	if part.Kinds[31] != diffmap.Added {
		t.Errorf("qty check kind = %v, want added", part.Kinds[31])
	}
	if part.Kinds[38] == diffmap.Same {
		t.Errorf("wrapped error line should differ from base")
	}
	if len(part.Ghosts) == 0 {
		t.Errorf("expected removed base lines inside Reserve")
	}

	_, audit := d.Station("audit")
	if !audit.Parts[0].NewFile {
		t.Errorf("audit.go should be a new file")
	}

	_, removed := d.Station("removed")
	rp := removed.Parts[0]
	if !rp.Base || rp.Err != nil {
		t.Fatalf("removed part: base=%v err=%v", rp.Base, rp.Err)
	}
	if rp.Kinds[rp.Range.Start] != diffmap.Removed {
		t.Errorf("ReleaseAll signature kind %v, want removed", rp.Kinds[rp.Range.Start])
	}
	removedLines := 0
	for i := rp.Range.Start; i <= rp.Range.End; i++ {
		if rp.Kinds[i] == diffmap.Removed {
			removedLines++
		}
	}
	// The closing brace pairs with the current file's last brace, which is a fair diff.
	if span := rp.Range.End - rp.Range.Start + 1; removedLines < span-1 {
		t.Errorf("ReleaseAll removed lines = %d of %d", removedLines, span)
	}

	_, tests := d.Station("tests")
	names := map[string]int{}
	for _, n := range tests.Tests[0].Names {
		names[n.Name] = n.Depth
	}
	if d, ok := names["takes stock"]; !ok || d != 1 {
		t.Errorf("subtest missing or wrong depth: %v", names)
	}
	if _, ok := names["TestAuditLogRecords"]; !ok {
		t.Errorf("top-level test missing: %v", names)
	}
}

func TestNoteLevelsAndStationFacts(t *testing.T) {
	d := build(t)
	_, store := d.Station("store")
	if issues, questions := store.Findings(); issues != 1 || questions != 0 {
		t.Fatalf("store findings = %d issues, %d questions", issues, questions)
	}
	_, reserve := d.Station("reserve")
	if loc := reserve.LOC(); loc != 15 {
		t.Errorf("reserve shows %d lines, want 15", loc)
	}

	unbacked := &doc.Note{}
	unbacked.Text = "⚠ this looks wrong"
	if unbacked.Level() != doc.KindIssue || !unbacked.Unbacked() {
		t.Error("a ⚠ note without evidence is an unbacked issue")
	}
	unbacked.Evidence = "TestX fails"
	if unbacked.Unbacked() {
		t.Error("evidence backs the issue")
	}

	_, tests := d.Station("tests")
	if !tests.Tests[0].NewFile {
		t.Error("stock_test.go does not exist on the base side")
	}
}

func TestLostAndAmbiguousAnchors(t *testing.T) {
	d := build(t)
	_, st := d.Station("reserve")
	cases := map[string]int{"s.mu.Lock()": 1, "nothing like this": 0}
	for needle, want := range cases {
		if got := len(st.Hits("", needle)); got != want {
			t.Errorf("hits(%q) = %d, want %d", needle, got, want)
		}
	}
	_, store := d.Station("store")
	if got := len(store.Hits("inventory/stock.go", "Store")); got < 2 {
		t.Errorf("expected Store to match several lines, got %d", got)
	}
}
