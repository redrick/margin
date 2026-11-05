package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/redrick/margin/internal/answers"
	"github.com/redrick/margin/internal/tmuxx"
)

// The agent's answers to the reader's comments, read in an overlay: scroll, jump from answer to
// answer, reply under one, or edit the note in $EDITOR.

type answersView struct {
	note *answers.Note
	at   int // the answer the reader is on, an index into note.IDs()
	top  int
	// replyTo is the comment a reply is being written for, while the footer asks for it.
	replyTo string
}

type answersEditedMsg struct{ err error }

func (m *Model) openAnswers() {
	n, err := answers.Load(m.opts.ReviewPath)
	if err != nil {
		m.setStatus(true, "%v", err)
		return
	}
	if !n.Exists {
		m.setStatus(false, "the agent has not answered yet: S sends your comments, and it writes its answers to %s", n.Path)
		return
	}
	at := 0
	if m.ans != nil {
		at = min(m.ans.at, max(len(n.IDs())-1, 0))
	}
	m.ans = &answersView{note: n, at: at}
}

// answerLines lays out the note at width w, each line with the comment id whose answer it is in.
func (m *Model) answerLines(w int) ([]string, []string) {
	n := m.ans.note
	ids := n.IDs()
	owner := func(off int) string {
		for _, id := range ids {
			if s := n.Sections[id]; off >= s[0] && off < s[1] {
				return id
			}
		}
		return ""
	}
	var lines, owners []string
	off := 0
	for raw := range strings.SplitSeq(n.Text, "\n") {
		id := owner(off)
		off += len(raw) + 1
		if raw == "" {
			lines, owners = append(lines, ""), append(owners, id)
			continue
		}
		for _, l := range wrap(raw, max(w-4, 10)) {
			lines, owners = append(lines, l), append(owners, id)
		}
	}
	return lines, owners
}

func (m *Model) answersEntries() []string {
	a := m.ans
	ids := a.note.IDs()
	cur := ""
	if a.at < len(ids) {
		cur = ids[a.at]
	}
	title := fmt.Sprintf(" answers to your comments · %d answered", len(ids))
	keys := " j k scroll · ] [ next / previous answer · r reply · e edit in $EDITOR · esc closes"
	out := []string{title, keys, ""}
	lines, owners := m.answerLines(min(peekWidth, m.width-4))
	room := max(m.bodyHeight()-len(out)-2, 3)
	a.top = max(min(a.top, len(lines)-room), 0)
	for i := a.top; i < len(lines) && i < a.top+room; i++ {
		mark := "  "
		if owners[i] != "" && owners[i] == cur {
			mark = "▌ "
		}
		out = append(out, " "+mark+lines[i])
	}
	return out
}

// jumpAnswer moves to the next or previous answer and scrolls to its heading.
func (m *Model) jumpAnswer(d int) {
	a := m.ans
	ids := a.note.IDs()
	if len(ids) == 0 {
		return
	}
	a.at = (a.at + d + len(ids)) % len(ids)
	_, owners := m.answerLines(min(peekWidth, m.width-4))
	for i, o := range owners {
		if o == ids[a.at] {
			a.top = i
			return
		}
	}
}

func (m *Model) updateAnswers(msg tea.KeyMsg) tea.Cmd {
	a := m.ans
	room := max(m.bodyHeight()-5, 3)
	switch msg.String() {
	case "j", "down":
		a.top++
	case "k", "up":
		a.top = max(a.top-1, 0)
	case "ctrl+d", "pgdown", " ":
		a.top += room / 2
	case "ctrl+u", "pgup":
		a.top = max(a.top-room/2, 0)
	case "g", "home":
		a.top = 0
	case "G", "end":
		a.top = 1 << 20
	case "]", "n", "tab":
		m.jumpAnswer(1)
	case "[", "N", "shift+tab":
		m.jumpAnswer(-1)
	case "r":
		ids := a.note.IDs()
		if len(ids) == 0 {
			m.setStatus(false, "no answer to reply to yet")
			return nil
		}
		if m.opts.AgentPane == "" {
			m.setStatus(false, "no agent pane to answer the reply; start margin-tui with the agent beside it")
			return nil
		}
		a.replyTo = ids[a.at]
		m.asking, m.commenting = true, false
		m.input.Prompt = " reply to " + a.replyTo + " › "
		m.input.Width = max(m.width-20, 10)
		m.input.SetValue("")
		m.input.Placeholder = "goes under the agent's answer in the note, and the agent answers it there"
		return m.input.Focus()
	case "e":
		path := a.note.Path
		return tea.ExecProcess(editorCmd(path), func(err error) tea.Msg { return answersEditedMsg{err: err} })
	case "esc", "q", "A":
		m.ans = nil
	}
	return nil
}

// sendReply writes the reply under the answer and asks the agent, in its pane, to answer it.
func (m *Model) sendReply(body string) tea.Cmd {
	a := m.ans
	id := a.replyTo
	a.replyTo = ""
	msg, err := answers.Reply(m.opts.ReviewPath, id, body)
	if err != nil {
		m.setStatus(true, "%v", err)
		return nil
	}
	m.openAnswers()
	pane, submit := m.opts.AgentPane, m.opts.Submit
	m.setStatus(false, "reply to %s saved; telling the agent…", id)
	return func() tea.Msg { return sentMsg{id: "reply to " + id, err: tmuxx.Send(pane, msg, submit)} }
}

func (m *Model) gotAnswersEdited(msg answersEditedMsg) {
	if msg.err != nil {
		m.setStatus(true, "editor: %v", msg.err)
	}
	if m.ans != nil {
		m.openAnswers()
	}
}

// editorCmd opens file in $VISUAL or $EDITOR, vi when neither is set.
func editorCmd(file string) *exec.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	args := append(strings.Fields(editor), file)
	return exec.Command(args[0], args[1:]...)
}
