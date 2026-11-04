package cli

import (
	"cmp"
	"errors"
	"fmt"
	"strings"

	"github.com/redrick/margin/internal/config"
)

const configHelp = `Settings, shared by margin-tui and margin-gui:

  reviews     where review files go
                repo     .margin/reviews in the repository, ignored by git (the default)
                state    ~/.local/state/margin
                <dir>    any directory; {project}, {topic} and {repo} are filled in. With
                         {project} or {topic} it is a notes folder: files are named margin-…
                         and start with frontmatter, e.g. ~/notes/{project}/topics/{topic}
  agent       the agent command; the instructions are its last argument or replace {prompt}
                claude (the default), codex, gemini -i {prompt}, opencode --prompt {prompt}
  agent_note  a line added to the agent's instructions; {project}, {topic} and {dir} are
              filled in, e.g. "File the review in the index of {project}."
  tests       how margin-gui runs the tests that cover a line, per repository; a list of
              repo (a folder, a glob or a repository name; empty matches all) and command,
              run by sh in the worktree with {pkg} {dir} {run} {tests} {file} filled in:
                tests:
                  - repo: ~/Projects/shop
                    command: docker compose run --rm app go test -v {dir} -run {run}
                  - command: go test -count=1 -v {dir} -run {run}
              Edit it in the file or in margin-gui's settings. margin never runs a command
              a repository brings along, only these.

  $MARGIN_DIR and $MARGIN_AGENT override the file.

usage: margin config [get [key] | set <key> <value> | unset <key> | path]
`

func cmdConfig(args []string) error {
	if len(args) == 0 {
		args = []string{"get"}
	}
	switch {
	case args[0] == "path" && len(args) == 1:
		fmt.Println(config.Path())
		return nil
	case args[0] == "help":
		fmt.Print(configHelp)
		return nil
	case args[0] == "get" && len(args) <= 2:
		c := config.Load()
		vals := map[string]string{"reviews": c.Reviews, "agent": c.Agent, "agent_note": c.AgentNote}
		if len(args) == 2 {
			v, ok := vals[args[1]]
			if !ok {
				return fmt.Errorf("unknown setting %q", args[1])
			}
			fmt.Println(v)
			return nil
		}
		fmt.Printf("# %s\nreviews: %s\nagent: %s\nagent_note: %s\n", config.Path(), c.Reviews, c.Agent, c.AgentNote)
		for _, t := range c.Tests {
			fmt.Printf("tests: %s → %s\n", cmp.Or(t.Repo, "*"), t.Command)
		}
		return nil
	case (args[0] == "set" && len(args) >= 3) || (args[0] == "unset" && len(args) == 2):
		c, err := config.File()
		if err != nil {
			return err
		}
		val := ""
		if args[0] == "set" {
			val = strings.Join(args[2:], " ")
		}
		switch args[1] {
		case "reviews":
			c.Reviews = val
		case "agent":
			c.Agent = val
		case "agent_note":
			c.AgentNote = val
		default:
			return fmt.Errorf("unknown setting %q; see margin config help", args[1])
		}
		return c.Save()
	}
	return errors.New(strings.TrimSpace(configHelp[strings.LastIndex(configHelp, "usage:"):]))
}
