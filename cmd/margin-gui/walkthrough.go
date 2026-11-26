package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"github.com/redrick/margin/internal/anchor"
	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/ignore"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/source"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/target"
)

type walkMode string

const (
	fileWalk  walkMode = "file"
	agentWalk walkMode = "agent"
)

var errWalkPreparing = errors.New("the agent walkthrough is still being prepared")

type WalkthroughView struct {
	Mode       walkMode         `json:"mode"`
	Ready      bool             `json:"ready"`
	Offer      bool             `json:"offer"`
	Position   *ReadingPosition `json:"position,omitempty"`
	FocusAreas []string         `json:"focusAreas"`
}

type walkthrough struct {
	mode           walkMode
	ready          bool
	legacyIDs      map[string]string
	alternate      *doc.Doc
	projectedNotes map[string]projectedNote
}

type projectedNote struct {
	ref      string
	highRisk bool
}

func fileStations(files *source.Files) ([]review.Station, error) {
	changes, err := files.Changed()
	if err != nil {
		return nil, err
	}
	return target.Stations(&target.Target{Changes: changes}, map[string]bool{}, ignore.Parse(files.RepoConfig("ignore"))), nil
}

// Legacy reviews have no checkpoint. Their generated stops carry no authored structure or rationale.
func hasAuthoredWalk(rv *review.Review, generated []review.Station) bool {
	for _, stop := range rv.Stations {
		if stop.Scope != "" || stop.What != "" || stop.Why != "" || stop.OrderWhy != "" {
			return true
		}
	}
	var files []string
	inWalk := make(map[string]bool, len(rv.Stations))
	for _, stop := range rv.Stations {
		if len(stop.Parts) != 1 {
			return true
		}
		p := stop.Parts[0]
		if !p.Hunks || p.Background {
			return true
		}
		files = append(files, p.File)
		inWalk[p.File] = true
	}
	var order []string
	for _, stop := range generated {
		if inWalk[stop.Parts[0].File] {
			order = append(order, stop.Parts[0].File)
		}
	}
	return len(files) > 0 && (len(rv.Skip) > 0 || !slices.Equal(files, order))
}

func beginWalkPreparation(rv *review.Review) (string, error) {
	if rv.Base == "" && rv.BaseDir == "" {
		return "", nil
	}
	saved := readReviewResume(rv.Path)
	if walkthroughReady(rv.Path, saved) {
		return "", nil
	}
	if saved.WalkPreparation == "" {
		files, err := source.Open(rv)
		if err != nil {
			return "", err
		}
		defer files.Close()
		generated, err := fileStations(files)
		if err != nil {
			return "", err
		}
		if hasAuthoredWalk(rv, generated) {
			return "", nil
		}
	}
	id := rand.Text()
	return id, updateReviewResume(rv.Path, func(record *reviewResume) bool {
		record.WalkPreparation = id
		return true
	})
}

func walkReadyPath(path string) string { return resumePath(path) + ".walkthrough-ready" }

func walkthroughReady(path string, saved reviewResume) bool {
	var ready string
	return saved.WalkPreparation != "" && readResumeJSON(walkReadyPath(path), &ready) == nil && ready == saved.WalkPreparation
}

// markWalkthroughReady is called by the GUI's margin shim. A file checkpoint also works when the
// reader is looking at a different review, or a terminal viewer owns the review's control socket.
func markWalkthroughReady(args []string) error {
	flags := flag.NewFlagSet("walkthrough-ready", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("review", "", "")
	preparation := flags.String("preparation", "", "")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !filepath.IsAbs(*path) || *preparation == "" || flags.NArg() != 0 {
		return errors.New("usage: margin walkthrough-ready --review FILE --preparation ID")
	}
	rv, err := review.Load(*path)
	if err != nil {
		return err
	}
	files, err := source.Open(rv)
	if err != nil {
		return err
	}
	defer files.Close()
	if !files.HasBase() {
		return errors.New("a comparison base is required to publish a walkthrough")
	}
	generated, err := fileStations(files)
	if err != nil {
		return err
	}
	if len(rv.Stations) == 0 || !hasAuthoredWalk(rv, generated) {
		return errors.New("save the functional grouping before publishing the walkthrough")
	}
	d := doc.Build(rv, files)
	for _, problem := range d.Problems {
		if problem.Station != "tests" {
			return fmt.Errorf("walkthrough %s: %s", problem.Station, problem.Msg)
		}
	}
	return lockResume(func() error {
		saved := readReviewResume(rv.Path)
		if saved.WalkPreparation != *preparation {
			return errors.New("this walkthrough preparation has been superseded")
		}
		if walkthroughReady(rv.Path, saved) {
			return nil
		}
		return writeResumeJSON(walkReadyPath(rv.Path), *preparation)
	})
}

func buildWalkthrough(rv *review.Review, files *source.Files, saved reviewResume, current, requested walkMode) (*doc.Doc, walkthrough, error) {
	if !files.HasBase() {
		if requested != "" {
			return nil, walkthrough{}, errors.New("walkthrough views require a comparison base")
		}
		return doc.Build(rv, files), walkthrough{}, nil
	}
	generated, err := fileStations(files)
	if err != nil {
		return nil, walkthrough{}, err
	}
	authored := hasAuthoredWalk(rv, generated)
	ready := authored && (walkthroughReady(rv.Path, saved) || saved.WalkPreparation == "")
	mode := current
	if requested != "" {
		if !ready && requested == agentWalk {
			return nil, walkthrough{}, errWalkPreparing
		}
		mode = requested
	}
	if mode == "" {
		mode = saved.WalkMode
		if mode == "" {
			mode = fileWalk
			if ready {
				mode = agentWalk
			}
		}
	}
	walk := walkthrough{mode: mode, ready: ready, legacyIDs: map[string]string{}}
	for i := range generated {
		stop := &generated[i]
		id := doc.FileStopID(stop.Parts[0].File)
		walk.legacyIDs[stop.ID] = id
		stop.ID = id
	}
	agent := doc.Build(rv, files)
	byFile := *rv
	byFile.Stations = generated
	byFile.Skip = nil
	d := doc.Build(&byFile, files)
	walk.projectedNotes = projectWalkNotes(d, agent)
	if mode == agentWalk {
		walk.alternate = d
		return agent, walk, nil
	}
	walk.alternate = agent
	return d, walk, nil
}

// Use the authored notes so a focus filter survives switching away from notes without file anchors.
func walkthroughFocusAreas(d *doc.Doc) []string {
	areas := []string{}
	for _, stop := range d.Stations {
		for _, note := range stop.Notes {
			if note.Focus != "" {
				areas = append(areas, note.Focus)
			}
		}
	}
	slices.Sort(areas)
	return slices.Compact(areas)
}

func projectWalkNotes(files, authored *doc.Doc) map[string]projectedNote {
	stops := make(map[string]*doc.Station)
	baseStops := make(map[string]*doc.Station)
	for _, stop := range files.Stations {
		stops[stop.ID] = stop
		for _, part := range stop.Parts {
			if part.Spec.BaseFile != "" {
				baseStops[part.Spec.BaseFile] = stop
			}
		}
	}
	notes := make(map[string]projectedNote)
	for _, stop := range authored.Stations {
		for i, note := range stop.Notes {
			if note.Part < 0 || note.Line < 0 || note.Problem != "" {
				continue
			}
			part := stop.Parts[note.Part]
			target := stops[doc.FileStopID(part.Spec.File)]
			if target == nil && part.Base {
				target = baseStops[part.Spec.File]
			}
			if target == nil {
				continue
			}
			n := *note
			n.Station = target.ID
			n.Part = partForWalkNote(target, part, note.Line)
			target.Notes = append(target.Notes, &n)
			notes[n.Key] = projectedNote{ref: fmt.Sprintf("%s:%d", stop.ID, i+1), highRisk: stop.Risk == "high"}
		}
	}
	return notes
}

func partForWalkNote(stop *doc.Station, source *doc.Part, line int) int {
	for i, part := range stop.Parts {
		if part.Err == nil && part.Spec.File == source.Spec.File && part.Base == source.Base &&
			part.Range.Start <= line && line <= part.Range.End {
			return i
		}
	}
	// A removed line in a modified file is shown as a ghost. Keep its actual base-side address
	// for note and comment actions, using a small excerpt after the file's generated diff.
	part := *source
	part.Range = anchor.Range{Start: line, End: line}
	part.Spec = review.Part{File: source.Spec.File, Side: source.Spec.Side, BaseFile: source.Spec.BaseFile, Lines: fmt.Sprint(line + 1)}
	part.Hunk, part.HunkCount = 0, 0
	stop.Parts = append(stop.Parts, &part)
	return len(stop.Parts) - 1
}

func migrateFileProgress(st *state.State, ids map[string]string) bool {
	changed := false
	for legacyID, fileID := range ids {
		if st.Visited[legacyID] {
			st.Visited[fileID] = true
			delete(st.Visited, legacyID)
			changed = true
		}
		if v := st.Verdicts[legacyID]; v != "" {
			if st.Verdicts[fileID] == "" {
				st.Verdicts[fileID] = v
			}
			delete(st.Verdicts, legacyID)
			changed = true
		}
	}
	return changed
}
