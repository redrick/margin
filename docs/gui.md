# margin-gui: the desktop app

[← margin](../README.md) · [the terminal viewer](tui.md)

margin-gui is margin in a desktop window. You add your repositories once, move between them, their
worktrees and branches in a sidebar, and read the review with the agent working in a terminal at
the bottom of the same window. It needs no tmux.

It reads and writes the same review files as [margin-tui](tui.md), so you can start a review in one
and go on in the other.

**Contents:** [Install](#install) · [Finding what to review](#finding-what-to-review) ·
[Reading a review](#reading-a-review) · [The agent](#the-agent) ·
[Answers to your comments](#answers-to-your-comments) · [Settings](#settings) · [Keys](#keys) ·
[Differences from margin-tui](#differences-from-margin-tui) · [Building](#building)

## Install

You need git and an agent CLI: [Claude Code](https://docs.claude.com/en/docs/claude-code)
(`claude`, the default), Codex, Gemini CLI, OpenCode, or any other agent that takes an initial
prompt and can run shell commands.

Download `margin-gui` for your system from the
[releases](https://github.com/redrick/margin/releases), or build it with Go 1.21 or newer (Go fetches
the toolchain it needs by itself). Building needs a C compiler and, on Linux, the GTK 4 and
WebKitGTK 6 development packages:

```sh
# Debian, Ubuntu 24.04+:  sudo apt install build-essential libgtk-4-dev libwebkitgtk-6.0-dev
# Fedora:                 sudo dnf install gcc gtk4-devel webkitgtk6.0-devel
# Arch:                   sudo pacman -S base-devel gtk4 webkitgtk-6.0
# macOS:                  xcode-select --install
go install github.com/redrick/margin/cmd/margin-gui@latest
```

Systems that only have the older WebKitGTK 4.1 (GTK 3) build it with `-tags gtk3`:

```sh
go install -tags gtk3 github.com/redrick/margin/cmd/margin-gui@latest
```

Then add it to your desktop's applications, so it starts from the launcher or the Dock like any
other app:

```sh
margin-gui --install      # Linux: a launcher entry and icon; macOS: ~/Applications/margin.app
margin-gui --uninstall    # takes them away again
```

The launcher starts margin-gui through your login shell, so it finds the agent and git on the same
`PATH` as your terminal. You can also run it from a terminal:

```sh
margin-gui                         # the window, with the repositories you added before
margin-gui ~/src                   # add a repository, or a folder of repositories, and open it
margin-gui ~/src/shop feature-x    # open the review of a branch or commit right away (. for uncommitted changes)
margin-gui x.review.yaml           # open a review file
```

## Finding what to review

The **sidebar** holds everything you work on:

- **Reviews in progress** lists the reviews you opened lately, with how many stops you judged, the
  drafts you have not sent and a green dot while an agent runs for them. Click one to go on.
- **Repositories** lists the folders you added with ＋. A folder can be a repository or hold several
  (found up to two levels deep). Each repository shows its worktrees with their branch, and a
  yellow `●` with the number of changed files where there is uncommitted work. The filter box
  narrows the list by repository, branch or path.

Click a worktree to see **what there is to review in it**:

- **Uncommitted changes** and **staged changes**, with the number of files.
- **This branch** against the main branch, with how many commits it is ahead and behind.
- **Reviews of this repository** that already exist, to pick up again.
- **Branches**, newest first, each with how far it is ahead and behind the main branch, the worktree
  it is checked out in, and whether a review of it is in progress. Tick *remote branches* to see
  those too.
- **Commits** on the current branch.
- **Pull requests**, when the GitHub CLI `gh` is installed: the open pull requests of the
  repository. A pull request whose head commit is not in your repository yet offers *Fetch and
  review*, which runs `git fetch origin pull/N/head` and opens it. This fetch is the only git
  command margin ever runs that changes your repository, and only when you click it.

Press **ctrl+k** anywhere to **go to** anything by typing a few letters of it: a review, a
repository or worktree, a branch of the worktree you are in, a pull request, or a command
(settings, add a repository, open a review file). What you picked lately comes first.

While a review is open, the repository's name left of its title (`shop ›`) takes you back to the
worktree page, and *Back to the review* returns. Each review keeps its own agent running, so you
can switch between reviews without stopping one.

## Reading a review

A review reads the same way as in the terminal viewer; [margin-tui](tui.md) describes it in detail,
with pictures. In short:

- **Orient → Walk → Decide.** The overview says what the change is for and what it does, shows the
  route through the stops with their risk and size, and asks whether the change makes sense done
  this way (`y`, `n`, `?`). Each stop is then walked one step at a time: its brief, code to know
  first, each change with the agent's notes, housekeeping, and the wrap-up where you give your
  verdict (`1` good, `2` needs changes, `3` not sure). `w` shows whole stops instead.
- **Your side.** Comment on a line with `c` (pick a kind: issue, suggestion, question, nitpick,
  praise, thought; `!` makes it blocking). Comments are drafts until `S` sends them all to the
  agent. Ask the agent about a line with `a`. Mark notes read, agree with a problem (`?`), dismiss
  one (`x`), tick the calls only you can make, and answer the agent's questions.
- **Seeing more.** Search (`/`), note filters (`F`), folding (`z`), removed lines (`d`), side by side
  (`s`), more context (`+` `-`) or the whole file (`f`), blind mode, callers and history around a
  stop, per-test coverage with the tests that run a line (`t`), a spotlight on one test, untested
  changes (`u`), the Tests stop, copying a note or a stop (`y` `Y`), and export for posting.
- **Before you approve** gathers your calls, the problems, open questions and your comments, and
  recommends approve, request changes or that the review is not finished yet.

The page reloads by itself whenever the agent changes the review, so notes appear as it writes them;
new ones are marked and `N` jumps to them.

## The agent

*Start agent* runs the agent from [Settings](#settings) in a terminal under the review, in the
repository, and asks it to start the review. Drag the line above it to give it more room; `` ` ``
puts the keyboard in it and `ctrl+esc` takes it out again.

The agent talks to the window with `margin` commands: `margin note` adds a note, `margin goto` shows
you a line, `margin where` tells it what you are looking at, `margin answer` and `margin resolve`
answer your questions and comments, `margin coverage` records which tests run which lines. These
commands are this same binary: margin-gui puts a `margin` link to itself first on the agent's
`PATH`, so you need no other margin installed. `MARGIN_CLI=1 margin-gui <command>` runs one by hand.

Untick *send messages with Enter* to have your questions, comments and test runs only typed into
the agent's prompt, so you can add to them before pressing Enter yourself.

If the same review is also open in margin-tui, the window says so: the agent's `margin goto` and
`margin where` then reach the terminal viewer, not the window.

## Answers to your comments

When you send your comments, the agent answers each one in a markdown note beside the review file
(`<review>.comments.md`) and closes it with a short answer on the line. *Answers* in the toolbar,
`A`, or *Answer* on a sent comment opens that note in the window:

- Each comment's answer is shown as its own section, with whether the comment is resolved.
- **Reply** under an answer writes your reply into the note under that answer and tells the agent,
  which answers below it. Use it to push back, ask a follow-up or agree.
- **Edit** (`e`) opens the whole note as text, to correct, shorten or annotate it; `ctrl+s` saves.

The panel updates while the agent writes, unless you are editing or replying.

## Settings

The gear in the sidebar (or **ctrl+,**) opens the settings. margin-gui and margin-tui share them,
in `~/.config/margin/config.yaml`.

- **Where reviews are kept.** *In the repository* (the default) keeps them in `.margin/reviews/`,
  which ignores itself in git and is shared by all worktrees of the repository. *A private folder*
  keeps them in `~/.local/state/margin`. *A folder of your choice* takes any directory, for
  example a notes vault, and may use `{project}` (the repository's name, or its `ai.memoryProject`
  git setting), `{topic}` (the branch) and `{repo}` (a unique folder per repository). With
  `{project}` or `{topic}` in it, files are named `margin-…` and start with frontmatter, so they sit
  well among other notes: `~/notes/{project}/topics/{topic}`.
- **Agent.** `claude`, `codex`, `gemini -i {prompt}`, `opencode --prompt {prompt}`, or another
  command. The instructions are passed as its last argument, or in place of `{prompt}`. Installed
  ones are marked.
- **Note for the agent.** A line added to the agent's instructions, with `{project}`, `{topic}` and
  `{dir}` filled in, for example "File the review in the index of {project}."
- **Agent skill.** Installs margin's skill for Claude Code or Codex, so an agent you start yourself
  knows margin too and can run a margin review when you ask for one.

`$MARGIN_DIR` and `$MARGIN_AGENT` still override the file, and the settings say so when they do.
Changed settings apply to reviews and agents started afterwards.

## Keys

`H` in a review lists every key. They follow margin-tui's; these are the ones only the window has:

| Key | What it does |
| --- | --- |
| `ctrl+k` | go to a review, repository, worktree, branch, pull request or command |
| `ctrl+,` | settings |
| `\` | show or hide the sidebar |
| `` ` `` | put the keyboard in the agent's terminal; `ctrl+esc` takes it out |
| `A` | the agent's answers to your comments |
| `j` `k` `enter` | on a worktree page: move through what to review and open it |
| `/` | on a worktree page: filter the branches, commits or pull requests |
| `1` `2` `3` | on a worktree page: branches, commits, pull requests |
| `r` | on a worktree page: read git again; in a review: reload |
| `esc` | on a worktree page: back to the open review |

## Differences from margin-tui

- No tmux: the agent runs in the window. `margin-tui split` and the tmux layouts have no
  counterpart.
- The list of stops is always shown, instead of behind `tab`.
- Notes are shown in full under their line, instead of in a column beside the code.
- Blind mode is a switch in the toolbar instead of the `--blind` flag, and *send messages with
  Enter* replaces `--no-submit`.
- The answers to your comments open in the window, where you can reply and edit; in margin-tui they
  are the markdown file beside the review.

## Building

From a checkout:

```sh
go build -o margin-gui ./cmd/margin-gui              # GTK 4 / WebKitGTK 6
go build -tags gtk3 -o margin-gui ./cmd/margin-gui   # WebKitGTK 4.1
```

The window is plain HTML, CSS and JavaScript in [`cmd/margin-gui/assets`](../cmd/margin-gui/assets),
embedded in the binary, with [xterm.js](https://xtermjs.org) for the agent's terminal; there is no
npm build step. The Go side is a few [Wails v3](https://v3.wails.io) services over margin's own
packages, so the review logic is the same code margin-tui runs.
