package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/testutil"
)

func TestReviewActionsUseDisplayedDocument(t *testing.T) {
	path := testutil.Example(t)
	r := &Reviews{path: path}
	v, err := r.Current()
	if err != nil {
		t.Fatal(err)
	}
	var note string
	for _, stop := range v.Stations {
		if len(stop.Notes) > 0 {
			note = stop.Notes[0].Key
			break
		}
	}
	if note == "" {
		t.Fatal("fixture has no notes")
	}

	if err := os.Remove(review.CoveragePath(path)); err != nil {
		t.Fatal(err)
	}
	update, err := r.MarkNote(note, "reviewed")
	if err != nil || update.Path != path || !slices.Contains(update.Reader.Reviewed, note) || update.Revision <= v.Revision {
		t.Fatalf("mark from the displayed document = %+v, %v", update, err)
	}
	saved, err := state.Load(review.StatePath(path))
	if err != nil || !saved.Reviewed[note] {
		t.Fatalf("note mark did not persist: %v", err)
	}
	saved.Dismissed[note] = true
	if err := saved.Save(); err != nil {
		t.Fatal(err)
	}
	update, err = r.Reader()
	if err != nil || !slices.Contains(update.Reader.Dismissed, note) {
		t.Fatalf("external reader update = %+v, %v", update, err)
	}
	ids, err := r.TestsFor("reserve", -1, v.Version)
	if err != nil || len(ids) == 0 {
		t.Fatalf("test indices from the displayed document = %v, %v", ids, err)
	}
	part, err := r.Lines("reserve", 0, 0, true, v.Version)
	if err != nil {
		t.Fatal(err)
	}
	if part.Start != 0 || part.End != part.Len-1 || len(part.Rows) < part.Len {
		t.Fatalf("whole file returned %d rows for lines %d–%d of %d", len(part.Rows), part.Start, part.End, part.Len)
	}

	next, err := r.Current()
	if err != nil || next.Coverage != nil || next.Version <= v.Version {
		t.Fatalf("refresh did not clear coverage or advance the version: %v", err)
	}
	if _, err := r.TestsFor("reserve", -1, v.Version); err == nil {
		t.Fatal("accepted stale test indices after refresh")
	}
	if _, err := r.Lines("reserve", 0, 0, true, v.Version); err == nil {
		t.Fatal("accepted a file expansion from the old view")
	}
	r.Close()
	if _, err := r.Reader(); err == nil {
		t.Fatal("reader update reused the closed document")
	}
}

func TestTryChangeChecksLiveSource(t *testing.T) {
	r := &Reviews{path: testutil.Example(t)}
	v, err := r.Current()
	if err != nil {
		t.Fatal(err)
	}
	ids, err := r.TestsFor("reserve", 0, v.Version)
	if err != nil || len(ids) == 0 {
		t.Fatalf("tests for reserve = %v, %v", ids, err)
	}
	old, err := r.LinesText("reserve", 0, 0, 0, v.Version)
	if err != nil {
		t.Fatal(err)
	}
	d, p, err := r.part("reserve", 0, 0, v.Version)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(d.Repo, p.Spec.File)
	live := "// different source\n"
	if err := os.WriteFile(file, []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = r.TryChange(TryRequest{Station: "reserve", Part: 0, Old: old, Text: "// attempted edit", Tests: ids}, v.Version)
	if err == nil || !strings.Contains(err.Error(), "on disk is not what the review shows") {
		t.Fatalf("try change did not detect changed source: %v", err)
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != live {
		t.Fatalf("try change touched the file: %q, %v", got, err)
	}
}
