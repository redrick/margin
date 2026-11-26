package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/redrick/margin/internal/fsx"
	"github.com/redrick/margin/internal/target"
)

const resumeVersion = 1

var errAgentInUse = errors.New("this review's agent is already open in another Margin window")

type ReadingPosition struct {
	Station string       `json:"station"`
	Mode    walkMode     `json:"mode,omitempty"`
	Step    *ReadingStep `json:"step,omitempty"`
	Scroll  int          `json:"scroll"`
}

type ReadingStep struct {
	Kind string `json:"kind"`
	File string `json:"file,omitempty"`
	Side string `json:"side,omitempty"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

type savedAgent struct {
	Command       string `json:"command"`
	Repo          string `json:"repo"`
	ID            string `json:"session_id,omitempty"`
	Run           string `json:"run"`
	PreparingWalk bool   `json:"preparing_walk,omitempty"`
	Auto          bool   `json:"resume_on_launch,omitempty"`
	Failed        bool   `json:"resume_failed,omitempty"`
}

type reviewResume struct {
	Version         int                           `json:"version"`
	Position        *ReadingPosition              `json:"position,omitempty"`
	Agent           *savedAgent                   `json:"agent,omitempty"`
	WalkChoice      bool                          `json:"walk_choice,omitempty"`
	WalkMode        walkMode                      `json:"walk_mode,omitempty"`
	WalkPositions   map[walkMode]*ReadingPosition `json:"walk_positions,omitempty"`
	WalkPreparation string                        `json:"walk_preparation,omitempty"`
	NoteVisibility  string                        `json:"note_visibility,omitempty"`
}

func validNoteVisibility(mode string) bool {
	switch mode {
	case "risky", "all", "hide":
		return true
	}
	return false
}

func (saved reviewResume) noteVisibility(blind bool) string {
	if validNoteVisibility(saved.NoteVisibility) {
		return saved.NoteVisibility
	}
	if blind {
		return "hide"
	}
	return "all"
}

type lastReview struct {
	Version int    `json:"version"`
	Path    string `json:"path"`
}

func guiStateDir() string {
	return filepath.Join(target.StateDir(), "gui")
}

func resumePath(path string) string {
	key := sha256.Sum256([]byte(resumeIdentity(path)))
	return filepath.Join(guiStateDir(), "reviews", fmt.Sprintf("%x.json", key))
}

func resumeIdentity(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	// Keep the lease key stable as missing review directories and the file are created.
	parent := filepath.Dir(path)
	if parent != path && errors.Is(err, os.ErrNotExist) {
		return filepath.Join(resumeIdentity(parent), filepath.Base(path))
	}
	return filepath.Clean(path)
}

func readResumeJSON(path string, value any) error {
	return readGUIStateJSON(path, value, 64<<10)
}

func readGUIStateJSON(path string, value any, limit int64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("saved review state must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return errors.New("saved review state is too large")
	}
	return json.Unmarshal(data, value)
}

func writeResumeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("saved review state must be a regular file")
		}
		if info.Mode().Perm() != 0o600 {
			if err := os.Chmod(path, 0o600); err != nil {
				return err
			}
		}
	}
	return fsx.WriteFileAtomic(path, append(data, '\n'), 0o600)
}

func readReviewResume(path string) reviewResume {
	var saved reviewResume
	if readResumeJSON(resumePath(path), &saved) != nil || saved.Version != resumeVersion {
		return reviewResume{Version: resumeVersion}
	}
	return saved
}

// Several Margin processes can save the same review's position and agent fields.
func lockResume(update func() error) error {
	dir := guiStateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		return err
	}
	return update()
}

func updateReviewResume(path string, update func(*reviewResume) bool) error {
	return lockResume(func() error {
		saved := readReviewResume(path)
		if !update(&saved) {
			return nil
		}
		return writeResumeJSON(resumePath(path), saved)
	})
}

func leaseAgent(path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("open a review before starting its agent")
	}
	lock := resumePath(path) + ".agent-lock"
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errAgentInUse
		}
		return nil, err
	}
	return f, nil
}

func startLeasedAgent(path string, launch func(*os.File) error) error {
	lease, err := leaseAgent(path)
	if err != nil {
		return err
	}
	if err := launch(lease); err != nil {
		lease.Close()
		return err
	}
	return nil
}

func clearReviewResume(path string) error {
	return lockResume(func() error {
		for _, file := range []string{resumePath(path), walkReadyPath(path)} {
			if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		return nil
	})
}

func lastReviewPath() string {
	var last lastReview
	if readResumeJSON(filepath.Join(guiStateDir(), "last-review.json"), &last) != nil ||
		last.Version != resumeVersion || !filepath.IsAbs(last.Path) {
		return ""
	}
	return last.Path
}

func forgetLastReview(path string) error {
	if lastReviewPath() == "" {
		return nil
	}
	return lockResume(func() error {
		if last := lastReviewPath(); last != "" && resumeIdentity(last) == resumeIdentity(path) {
			return os.Remove(filepath.Join(guiStateDir(), "last-review.json"))
		}
		return nil
	})
}

func (saved reviewResume) positionFor(walk walkthrough) *ReadingPosition {
	if walk.mode == "" {
		return saved.Position
	}
	if p := saved.WalkPositions[walk.mode]; p != nil {
		return p
	}
	// The old position belongs to the layout that existed before either view was used.
	if saved.Position == nil || len(saved.WalkPositions) > 0 {
		return nil
	}
	position := *saved.Position
	position.Mode = walk.mode
	if walk.mode == fileWalk {
		if id := walk.legacyIDs[position.Station]; id != "" {
			position.Station = id
		}
	}
	return &position
}

func (r *Reviews) ReadingPosition(path string) *ReadingPosition {
	r.mu.Lock()
	defer r.mu.Unlock()
	return readReviewResume(path).positionFor(r.walk)
}

func (r *Reviews) SaveReadingPosition(path string, position ReadingPosition, remember bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.path != path || position.Station == "" || position.Mode != r.walk.mode {
		return nil
	}
	return lockResume(func() error {
		saved := readReviewResume(path)
		if position.Mode == "" {
			saved.Position = &position
		} else {
			if saved.WalkPositions == nil {
				saved.WalkPositions = make(map[walkMode]*ReadingPosition)
			}
			saved.WalkPositions[position.Mode] = &position
		}
		if err := writeResumeJSON(resumePath(path), saved); err != nil {
			return err
		}
		if remember {
			last := lastReview{Version: resumeVersion, Path: path}
			return writeResumeJSON(filepath.Join(guiStateDir(), "last-review.json"), last)
		}
		return nil
	})
}
