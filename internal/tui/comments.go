package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/tmuxx"
)

// startComment opens the prompt for a review comment on the cursor line. Comments stay drafts
// until S sends them all to the agent in one message.
func (m *Model) startComment() tea.Cmd {
	r, ok := m.cursorRow()
	if !ok {
		m.setStatus(false, "move the cursor onto a code line to comment on it")
		return nil
	}
	p := m.doc.Stations[m.station].Parts[r.part]
	m.asking, m.commenting = true, true
	m.input.Prompt = " comment › "
	m.input.SetValue("")
	m.input.Placeholder = fmt.Sprintf("comment on %s:%d, kept as a draft until S sends them, esc cancels", p.Spec.File, r.line+1)
	return m.input.Focus()
}

// lineQuestion describes the cursor line the way questions and comments anchor to it.
func (m *Model) lineQuestion(id, text string) (state.Question, bool) {
	st := m.doc.Stations[m.station]
	r, ok := m.cursorRow()
	if !ok {
		return state.Question{}, false
	}
	p := st.Parts[r.part]
	line := r.line
	needle := strings.TrimSpace(p.Lines[line])
	for needle == "" && line > p.Range.Start {
		line--
		needle = strings.TrimSpace(p.Lines[line])
	}
	return state.Question{
		ID:      id,
		Station: st.ID,
		File:    p.Spec.File,
		Side:    p.Spec.Side,
		Line:    r.line + 1,
		Needle:  needle,
		Text:    text,
		Asked:   time.Now().UTC().Truncate(time.Second),
	}, true
}

func (m *Model) comment(text string) {
	q, ok := m.lineQuestion(m.state.NextCommentID(), text)
	if !ok {
		return
	}
	q.Kind, q.Draft = state.KindComment, true
	m.state.Questions = append(m.state.Questions, q)
	if err := m.state.Save(); err != nil {
		m.setStatus(true, "saving comment: %v", err)
		return
	}
	m.rebuildKeep()
	m.setStatus(false, "%s saved as a draft; S sends every draft to the agent", q.ID)
}

func (m *Model) drafts() []state.Question {
	var out []state.Question
	if m.state == nil {
		return nil
	}
	for _, q := range m.state.Questions {
		if q.IsComment() && q.Draft {
			out = append(out, q)
		}
	}
	return out
}

// sendComments hands every draft to the agent in one message and marks them sent.
func (m *Model) sendComments() tea.Cmd {
	drafts := m.drafts()
	if len(drafts) == 0 {
		m.setStatus(false, "no draft comments; c writes one on the cursor line")
		return nil
	}
	if m.opts.AgentPane == "" {
		m.setStatus(false, "no agent pane; the drafts stay until the viewer runs beside an agent")
		return nil
	}
	for i := range m.state.Questions {
		if m.state.Questions[i].IsComment() {
			m.state.Questions[i].Draft = false
		}
	}
	if err := m.state.Save(); err != nil {
		m.setStatus(true, "saving comments: %v", err)
		return nil
	}
	m.rebuildKeep()
	pane, submit, msg := m.opts.AgentPane, m.opts.Submit, CommentsMessage(drafts)
	id := fmt.Sprintf("%d comments", len(drafts))
	if len(drafts) == 1 {
		id = drafts[0].ID
	}
	ids := make([]string, len(drafts))
	for i, q := range drafts {
		ids[i] = q.ID
	}
	m.setStatus(false, "sending %s…", id)
	return func() tea.Msg { return sentMsg{id: id, err: tmuxx.Send(pane, msg, submit), drafts: ids} }
}

func (m *Model) redraft(ids []string) {
	if len(ids) == 0 {
		return
	}
	for i := range m.state.Questions {
		for _, id := range ids {
			if m.state.Questions[i].ID == id {
				m.state.Questions[i].Draft = true
			}
		}
	}
	m.state.Save()
	m.rebuildKeep()
}

// CommentsMessage is the one line the agent receives for a batch of review comments.
func CommentsMessage(qs []state.Question) string {
	parts := make([]string, len(qs))
	for i, q := range qs {
		parts[i] = fmt.Sprintf("%s %s:%d (station %s, line `%s`): %s", q.ID, q.File, q.Line, q.Station, doc.Short(q.Needle, 60), q.Text)
	}
	return fmt.Sprintf("[margin comments] %d review comments from the reader. Handle each: change the code or explain why not, then close it with margin resolve <id> \"what you did\". %s",
		len(qs), strings.Join(parts, " | "))
}

// deleteComment removes a draft picked in the recap; sent comments stay, since the agent has them.
func (m *Model) deleteComment() bool {
	if m.cur < 0 || m.cur >= len(m.rows) || m.rows[m.cur].kind != rowLink || m.rows[m.cur].comment < 0 {
		return false
	}
	i := m.rows[m.cur].comment
	if i >= len(m.state.Questions) {
		return true
	}
	q := m.state.Questions[i]
	if !q.Draft {
		m.setStatus(false, "%s was sent already; ask the agent to drop it", q.ID)
		return true
	}
	m.state.Questions = append(m.state.Questions[:i:i], m.state.Questions[i+1:]...)
	if err := m.state.Save(); err != nil {
		m.setStatus(true, "saving comments: %v", err)
		return true
	}
	m.rebuildKeep()
	m.setStatus(false, "%s deleted", q.ID)
	return true
}

// commentsAt lists the reader's open comments anchored on a line of the current stop.
func (m *Model) commentsAt(p *doc.Part, line int) []state.Question {
	if m.state == nil {
		return nil
	}
	st := m.doc.Stations[m.station]
	resolved := m.doc.AnsweredQuestions()
	var out []state.Question
	for _, q := range m.state.Questions {
		if q.IsComment() && !resolved[q.ID] && q.Station == st.ID && q.File == p.Spec.File &&
			q.Line == line+1 && (q.Side == "base") == p.Base {
			out = append(out, q)
		}
	}
	return out
}
