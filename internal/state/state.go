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
	Questions []Question        `yaml:"questions,omitempty"`

	path string
}

type Question struct {
	ID      string    `yaml:"id" json:"id"`
	Station string    `yaml:"station" json:"station"`
	File    string    `yaml:"file" json:"file"`
	Side    string    `yaml:"side,omitempty" json:"side,omitempty"`
	Line    int       `yaml:"line" json:"line"`
	Needle  string    `yaml:"needle" json:"needle"`
	Text    string    `yaml:"text" json:"text"`
	Asked   time.Time `yaml:"asked" json:"asked"`
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

func (s *State) NextQuestionID() string {
	n := 0
	for _, q := range s.Questions {
		if v, err := strconv.Atoi(strings.TrimPrefix(q.ID, "q")); err == nil && v > n {
			n = v
		}
	}
	return "q" + strconv.Itoa(n+1)
}

func (s *State) Question(id string) (Question, bool) {
	for _, q := range s.Questions {
		if q.ID == id {
			return q, true
		}
	}
	return Question{}, false
}
