package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/redrick/margin/internal/fsx"
)

type State struct {
	Reviewed  map[string]bool   `yaml:"reviewed,omitempty"`
	Flagged   map[string]bool   `yaml:"flagged,omitempty"`
	Dismissed map[string]bool   `yaml:"dismissed,omitempty"`
	Seen      map[string]string `yaml:"seen,omitempty"`
	Revealed  map[string]bool   `yaml:"revealed,omitempty"`
	Visited   map[string]bool   `yaml:"visited,omitempty"`
	// Verdicts holds the reader's verdict on each stop they finished: good, changes or unsure.
	Verdicts map[string]string `yaml:"verdicts,omitempty"`
	// Full is set when the reader switched from the guided walk to seeing each stop whole.
	Full bool `yaml:"full_view,omitempty"`
	// Blind hides the agent's notes on high-risk stops until the reader revealed them.
	Blind bool `yaml:"blind,omitempty"`
	// Orient is the reader's answer to whether the change makes sense as done: yes, no or unsure;
	// Design is what they wrote when the answer was no.
	Orient    string     `yaml:"orient,omitempty"`
	Design    string     `yaml:"design,omitempty"`
	Questions []Question `yaml:"questions,omitempty"`

	path string
}

// Question is something the reader raised on a line: a question for the agent (id qN), or a review
// comment (id cN, kind comment) that stays a draft until the reader sends the batch.
type Question struct {
	ID      string    `yaml:"id" json:"id"`
	Kind    string    `yaml:"kind,omitempty" json:"kind,omitempty"`
	Draft   bool      `yaml:"draft,omitempty" json:"draft,omitempty"`
	Station string    `yaml:"station" json:"station"`
	File    string    `yaml:"file" json:"file"`
	Side    string    `yaml:"side,omitempty" json:"side,omitempty"`
	Line    int       `yaml:"line" json:"line"`
	Needle  string    `yaml:"needle" json:"needle"`
	Text    string    `yaml:"text" json:"text"`
	Asked   time.Time `yaml:"asked" json:"asked"`
	// Answers is the key of the agent's question note this comment answers.
	Answers string `yaml:"answers,omitempty" json:"answers,omitempty"`
}

func Load(path string) (*State, error) {
	s := &State{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s.init(), nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s.init(), nil
}

func (s *State) init() *State {
	if s.Reviewed == nil {
		s.Reviewed = map[string]bool{}
	}
	if s.Flagged == nil {
		s.Flagged = map[string]bool{}
	}
	if s.Seen == nil {
		s.Seen = map[string]string{}
	}
	if s.Verdicts == nil {
		s.Verdicts = map[string]string{}
	}
	for _, m := range []*map[string]bool{&s.Dismissed, &s.Revealed, &s.Visited} {
		if *m == nil {
			*m = map[string]bool{}
		}
	}
	return s
}

func (s *State) Save() error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return fsx.WriteFileAtomic(s.path, data, 0o600)
}

func (s *State) Path() string { return s.path }

const KindComment = "comment"

func (s *State) NextQuestionID() string { return s.nextID("q") }

func (s *State) NextCommentID() string { return s.nextID("c") }

func (s *State) nextID(prefix string) string {
	n := 0
	for _, q := range s.Questions {
		rest, ok := strings.CutPrefix(q.ID, prefix)
		if v, err := strconv.Atoi(rest); ok && err == nil && v > n {
			n = v
		}
	}
	return prefix + strconv.Itoa(n+1)
}

func (q Question) IsComment() bool { return q.Kind == KindComment }

func (s *State) Question(id string) (Question, bool) {
	for _, q := range s.Questions {
		if q.ID == id {
			return q, true
		}
	}
	return Question{}, false
}
