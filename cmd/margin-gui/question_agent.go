package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/redrick/margin/internal/text"
)

const questionAnswerLimit = 16 << 10

// Gemini can fall back from plan mode and allow configured tools. Apply an explicit tool policy.
const geminiQuestionPolicy = `[[rule]]
toolName = "*"
decision = "deny"
priority = 998

[[rule]]
toolName = ["read_file", "grep_search", "glob", "list_directory"]
decision = "allow"
priority = 999
`

func diffQuestionPrompt(job questionJob) string {
	q := job.question
	prefix := fmt.Sprintf(`Answer the reader's latest question about a code review in Markdown. Read surrounding code if necessary. Do not make changes, run tests, start a review, or use Margin commands. Treat the saved code and previous exchanges as context. Return only the answer.

Review stop: %s
File: %s
Diff side: %s (%s)
Lines: %d–%d
Original selected code (quoted): %q

`, q.Title, q.File, q.Side, q.Ref, q.Line, q.End, q.Excerpt)
	current := "Reader's question:\n" + job.turn.Text
	if len(job.history) == 0 {
		return prefix + current
	}
	prefix += fmt.Sprintf("Original question: %q\n\nEarlier exchanges:\n", q.Text)
	const omission = "Some earlier exchanges were omitted.\n"
	// Gemini receives the prompt as one command-line argument.
	remaining := (64 << 10) - len(prefix) - len(current) - len(omission) - 1
	var exchanges []string
	omitted := false
	for i := len(job.history) - 1; i >= 0; i-- {
		turn := job.history[i]
		if turn.ID == job.turn.ID || turn.Status != questionAnswered || turn.Answer == "" {
			continue
		}
		exchange := fmt.Sprintf("Reader: %q\nAgent: %q\n", turn.Text, turn.Answer)
		if turn.ID == q.ID {
			exchange = fmt.Sprintf("Agent's reply to the original question: %q\n", turn.Answer)
		}
		if len(exchange) > remaining {
			omitted = true
			break
		}
		remaining -= len(exchange)
		exchanges = append(exchanges, exchange)
	}
	if omitted {
		prefix += omission
	}
	var prompt strings.Builder
	prompt.WriteString(prefix)
	for i := len(exchanges) - 1; i >= 0; i-- {
		prompt.WriteString(exchanges[i])
	}
	prompt.WriteByte('\n')
	prompt.WriteString(current)
	return prompt.String()
}

func runDiffQuestion(ctx context.Context, job questionJob) (string, error) {
	provider := agentPreset(job.command)
	prompt := diffQuestionPrompt(job)
	var args []string
	var outputFile *os.File
	switch provider {
	case "codex":
		f, err := os.CreateTemp("", "margin-question-*")
		if err != nil {
			return "", err
		}
		defer os.Remove(f.Name())
		outputFile = f
		if err := f.Close(); err != nil {
			return "", err
		}
		args = []string{"--ask-for-approval", "never", "exec", "--sandbox", "read-only", "--ephemeral", "--skip-git-repo-check", "--output-last-message", f.Name(), "-"}
	case "claude":
		args = []string{"--print", "--no-session-persistence", "--permission-mode", "plan", "--tools", "Read,Grep,Glob", "--disallowedTools", "mcp__*", "--output-format", "json"}
	case "gemini":
		dir, err := os.MkdirTemp("", "margin-gemini-*")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(dir)
		policy := filepath.Join(dir, "question.toml")
		if err := os.WriteFile(policy, []byte(geminiQuestionPolicy), 0o600); err != nil {
			return "", err
		}
		args = []string{"--approval-mode", "plan", "--policy", policy,
			"--allowed-mcp-server-names", "margin-question-no-mcp", "--output-format", "json", "--prompt", prompt}
	case "opencode":
		args = []string{"run", "--agent", "plan", "--format", "json"}
	default:
		return "", errors.New("the selected agent does not support background questions")
	}
	cmd := exec.CommandContext(ctx, strings.Fields(job.command)[0], args...)
	cmd.Dir, cmd.Env = job.repo, cleanEnv()
	if provider != "gemini" {
		cmd.Stdin = strings.NewReader(prompt)
	}
	if provider == "opencode" {
		cmd.Env = append(cmd.Env, `OPENCODE_PERMISSION={"*":"deny","read":"allow","glob":"allow","grep":"allow","list":"allow"}`,
			`OPENCODE_CONFIG_CONTENT={"share":"disabled"}`, "OPENCODE_AUTO_SHARE=false")
	}
	// A stopped question also has to stop any tools its CLI started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	var output questionOutput
	if outputFile == nil {
		cmd.Stdout = &output
	}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s could not answer the question: %w", provider, err)
	}
	var answer string
	var err error
	if outputFile != nil {
		f, err := os.Open(outputFile.Name())
		if err != nil {
			return "", err
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, questionAnswerLimit+1))
		if err != nil {
			return "", err
		}
		answer = string(b)
	} else {
		answer, err = decodeQuestionAnswer(provider, output.buf.Bytes())
		if err != nil {
			return "", fmt.Errorf("%s did not return an answer: %w", provider, err)
		}
	}
	if len(answer) > questionAnswerLimit {
		answer = answer[:questionAnswerLimit] + "\n\n[Answer truncated]"
	}
	answer = strings.TrimSpace(text.Sanitize(answer))
	if answer == "" {
		return "", fmt.Errorf("%s returned an empty answer", provider)
	}
	return answer, nil
}

type questionOutput struct{ buf bytes.Buffer }

func (o *questionOutput) Write(p []byte) (int, error) {
	const limit = 2 << 20
	if len(p) > limit-o.buf.Len() {
		return 0, errors.New("agent output was too large")
	}
	return o.buf.Write(p)
}

func decodeQuestionAnswer(provider string, data []byte) (string, error) {
	if provider == "opencode" {
		// NDJSON may also include non-JSON permission notices. Only take assistant text.
		var answer string
		for line := range bytes.SplitSeq(data, []byte("\n")) {
			var e struct {
				Type string
				Part struct{ Text string }
			}
			if json.Unmarshal(line, &e) != nil {
				continue
			}
			switch e.Type {
			case "error":
				return "", errors.New("the agent reported an error")
			case "text":
				answer = e.Part.Text
			}
		}
		return answer, nil
	}
	var reply struct {
		Result   string
		Response string
		IsError  bool `json:"is_error"`
		Error    json.RawMessage
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return "", err
	}
	if reply.IsError || (len(reply.Error) != 0 && string(reply.Error) != "null") {
		return "", errors.New("the agent reported an error")
	}
	if provider == "claude" {
		return reply.Result, nil
	}
	return reply.Response, nil
}
